package session

import (
	"air_orchestrator/internal/metrics"
	"context"
	"fmt"

	"github.com/ikermy/air_common/pkg/comdb"
	"github.com/ikermy/air_common/pkg/endpoint"
	"github.com/ikermy/air_common/pkg/model"
	"github.com/ikermy/air_common/pkg/model/commdom"
	"github.com/ikermy/air_common/pkg/model/create"
	"github.com/ikermy/air_common/pkg/startpoint"

	"sync"
	"time"

	"github.com/ikermy/air_logger/v2/pkg/logger"
)

//const DayLimitForDemoUser = 10

// Store — минимальный интерфейс к БД для TestAPI.
type Store interface {
	CheckDemo(userId uint32) (bool, error)
	GetOrSetTreadAndResponder(userID uint32, responderRealId uint64, responderName string, chatType comdb.ChatType) (uint64, error)
	GetModelByProviderAnyStatus(userID uint32, provider commdom.ProviderType) (*commdom.UserModelRecord, error)
}

// ============================================================================
// ЗАГЛУШКИ ДЛЯ STARTPOINT
// ============================================================================

// NullBot заглушка для BotInterface
type NullBot struct{}

func (n *NullBot) StartBots() error { return nil }
func (n *NullBot) StopBot()         {}
func (n *NullBot) DisableOperatorMode(_ uint32, _ uint64, _ ...bool) error {
	return nil
}

// NullOperator заглушка для OperatorInterface
type NullOperator struct{}

func (n *NullOperator) AskOperator(_ context.Context, _ uint32, _ uint64, _ model.Message) (model.Message, error) {
	return model.Message{}, fmt.Errorf("operator not available in test mode")
}

func (n *NullOperator) SendToOperator(_ context.Context, _ uint32, _ uint64, _ model.Message) error {
	return fmt.Errorf("operator not available in test mode")
}

func (n *NullOperator) ReceiveFromOperator(_ context.Context, _ uint32, _ uint64) <-chan model.Message {
	ch := make(chan model.Message)
	close(ch)
	return ch
}

func (n *NullOperator) DeleteSession(_ uint32, _ uint64) error {
	return nil
}

func (n *NullOperator) GetConnectionErrors(_ context.Context, _ uint32, _ uint64) <-chan string {
	ch := make(chan string)
	close(ch)
	return ch
}

func (n *NullOperator) CloseOperatorSSE(_ context.Context, _ uint32, _ uint64) error {
	return nil
}

// ============================================================================
// ТИПЫ ДАННЫХ
// ============================================================================

// TestSession представляет активную тестовую сессию
type TestSession struct {
	UserId     uint32
	RespId     uint64
	TreadId    uint64
	Channel    *model.Ch
	RespModel  *model.RespModel
	StartedAt  time.Time
	LastUsedAt time.Time
	Cancel     context.CancelFunc
	IsDemo     bool // Является ли пользователь демо-пользователем
	mu         sync.RWMutex
}

// TestAPI управляет тестовыми сессиями
type TestAPI struct {
	sessions sync.Map // map[string]*TestSession
	starter  *startpoint.Start
	mod      *model.Router
	store    Store
	end      *endpoint.Endpoint
	ttl      time.Duration
	mu       sync.RWMutex
	ctx      context.Context // контекст для управления жизненным циклом фоновых горутин
}

func (ta *TestAPI) getUserModel(userId uint32, provider commdom.ProviderType) (*commdom.UniversalModelData, error) {
	userModel, err := ta.mod.GetUserModelByProvider(userId, provider)
	if err != nil {
		return nil, fmt.Errorf("ошибка получения активной модели пользователя: %w", err)
	}
	// userModel != nil гарантируется методом GetUserModelByProvider
	return userModel, nil
}

// ============================================================================
// API СТРУКТУРА
// ============================================================================

// API главная структура модуля API
type API struct {
	ctx     context.Context
	cancel  context.CancelFunc
	testAPI *TestAPI
}

// New создает новый экземпляр API модуля
func New(parent context.Context, mod *model.Router, db Store, end *endpoint.Endpoint) *API {
	ctx, cancel := context.WithCancel(parent)

	// Создаем заглушки для bot и operator
	nullBot := &NullBot{}
	nullOp := &NullOperator{}

	// Создаем startpoint.Start
	starter := startpoint.New(ctx, mod, end, nullBot, nullOp)

	testAPI := NewTestAPI(ctx, starter, mod, db, end, 15*time.Minute)

	api := &API{
		ctx:     ctx,
		cancel:  cancel,
		testAPI: testAPI,
	}

	return api
}

// GetTestAPI возвращает экземпляр TestAPI для использования в web handlers
func (a *API) GetTestAPI() *TestAPI {
	return a.testAPI
}

// Shutdown останавливает API сервер
func (a *API) Shutdown() {
	if a.cancel != nil {
		a.cancel()
	}
	logger.Info("API: сервер остановлен")
}

// ============================================================================
// TESTAPI МЕТОДЫ
// ============================================================================

// NewTestAPI создает новый экземпляр TestAPI
func NewTestAPI(
	ctx context.Context,
	starter *startpoint.Start,
	mod *model.Router,
	store Store,
	end *endpoint.Endpoint,
	ttl time.Duration,
) *TestAPI {
	api := &TestAPI{
		starter: starter,
		mod:     mod,
		store:   store,
		end:     end,
		ttl:     ttl,
		ctx:     ctx,
	}

	go api.cleanupLoop()

	return api
}

// sessionKey генерирует ключ для идентификации сессии
func (ta *TestAPI) sessionKey(userId uint32, respId uint64) string {
	return fmt.Sprintf("%d:%d", userId, respId)
}

// StartSession запускает новую тестовую сессию
func (ta *TestAPI) StartSession(ctx context.Context, userId uint32, respId uint64, provider commdom.ProviderType) (*TestSession, *commdom.UniversalModelData, error) {
	key := ta.sessionKey(userId, respId)

	if existing, ok := ta.sessions.Load(key); ok {
		session := existing.(*TestSession)

		// StopSession может закрыть каналы, пока запись о RespModel ещё
		// доступна для повторного подключения. Такой канал переиспользовать
		// нельзя: WebSocket сразу получит закрытый TxCh.
		if session.Channel == nil || !session.Channel.IsTxOpen() || !session.Channel.IsRxOpen() {
			ta.sessions.Delete(key)
			if session.RespModel != nil && session.RespModel.Chan != nil {
				delete(session.RespModel.Chan, session.TreadId)
			}
			metrics.ActiveTestSessions.Dec()
			ok = false
		} else {

			// Проверяем, что provider совпадает
			if session.RespModel.Assist.Provider != provider {
				return nil, nil, fmt.Errorf("сессия уже существует для другого провайдера (существующий: %s, запрошенный: %s)",
					session.RespModel.Assist.Provider.String(), provider.String())
			}

			session.mu.Lock()
			session.LastUsedAt = time.Now()
			session.mu.Unlock()
			logger.Debug("TestAPI: сессия уже существует для respId=%d", respId, userId)

			userModel, err := ta.getUserModel(userId, provider)
			if err != nil {
				logger.Error("TestAPI: ошибка получения активной модели пользователя для существующей сессии respId=%d: %v", respId, err, userId)
				return nil, nil, fmt.Errorf("ошибка получения активной модели пользователя: %w", err)
			}

			return session, userModel, nil
		}
	}

	// Получаем или создаем treadId (dialogId) через GetOrSetTreadAndResponder
	// т.к. операция с БД запускаю в отдельной горутине
	type resultTread struct {
		treadId uint64
		err     error
	}
	resultCh := make(chan resultTread, 1)
	go func() {
		defer close(resultCh)

		treadId, err := ta.store.GetOrSetTreadAndResponder(userId, respId, "test_responder", comdb.Web) // ChatType = web (1)
		resultCh <- resultTread{treadId, err}
	}()

	// Проверяем, является ли пользователь демо-пользователем долгая операция с БД, поэтому вынесена в отдельную горутину
	isDemoCh := make(chan bool, 1)
	go func() {
		defer close(isDemoCh)

		isDemo, err := ta.store.CheckDemo(userId)
		if err != nil {
			logger.Warn("TestAPI: ошибка проверки демо-статуса: %v, считаем НЕ демо", err, userId)
			isDemo = false // В случае ошибки считаем пользователя не демо
		}
		isDemoCh <- isDemo
	}()

	// Получаем активную модель пользователя через
	userModel, err := ta.getUserModel(userId, provider)
	if err != nil {
		return nil, nil, fmt.Errorf("ошибка получения активной модели пользователя: %w", err)
	}
	logger.Debug("TestAPI: конфигурация модели загружена, userId=%d, provider=%s, model=%s, web_search=%v, search=%v, interpreter=%v",
		userId, provider.String(), userModel.Name, userModel.WebSearch, userModel.Search, userModel.Interpreter)

	// Получаем запись о модели из БД для получения AssistantId в горутине, т.к. это долгая операция с БД
	type resultModelRecord struct {
		modelRecord *commdom.UserModelRecord
		err         error
	}
	resModelCh := make(chan resultModelRecord, 1)
	go func() {
		defer close(resModelCh)
		modelRecord, err := ta.store.GetModelByProviderAnyStatus(userId, userModel.Provider)
		resModelCh <- resultModelRecord{modelRecord, err}
	}()

	// Ждем завершения горутин параллельно
	var treadId uint64
	var modelRecord *commdom.UserModelRecord
	var isDemo bool
	var completedCount int

	for completedCount < 3 {
		select {
		case <-ctx.Done():
			return nil, nil, fmt.Errorf("контекст отменен при инициализации сессии")

		case result := <-resultCh:
			if result.err != nil {
				return nil, nil, fmt.Errorf("ошибка получения treadId: %w", result.err)
			}
			treadId = result.treadId
			resultCh = nil // Предотвращаем повторное чтение из закрытого канала
			completedCount++

		case result := <-resModelCh:
			if result.err != nil {
				return nil, nil, fmt.Errorf("ошибка получения записи модели: %w", result.err)
			}
			modelRecord = result.modelRecord
			resModelCh = nil // Предотвращаем повторное чтение из закрытого канала
			completedCount++

		case demo := <-isDemoCh:
			isDemo = demo
			isDemoCh = nil // Предотвращаем повторное чтение из закрытого канала
			completedCount++
		}
	}

	// Создаем Assistant из данных модели
	assist := model.Assistant{
		UserID:     userId,
		AssistId:   modelRecord.AssistId,
		AssistName: userModel.Name,
		Provider:   userModel.Provider,
		Metas: model.Target{
			MetaAction: userModel.MetaAction,
			Triggers:   userModel.Triggers,
		},
		Events: model.Notifications{
			Start:  false, // Устанавливаем по умолчанию
			End:    false,
			Target: false,
		},
	}
	logger.Debug("TestAPI: создаю Assistant, userId=%d, respId=%d, provider=%s, web_search=%v",
		userId, respId, userModel.Provider.String(), userModel.WebSearch)

	// Обрабатываем Espero настройки
	assist.Limit = uint32(userModel.Espero.Limit)
	assist.Espero = userModel.Espero.Wait
	assist.Ignore = userModel.Espero.Ignore

	respModel, err := ta.mod.GetOrSetRespGPT(assist, treadId, respId, "test_responder")
	if err != nil {
		return nil, nil, fmt.Errorf("ошибка получения RespModel: %w", err)
	}

	// При создании новой сессии создаем НОВЫЙ контекст!
	// Старый контекст из respModel может быть отменен если это пересоздание сессии
	sessCtx, sessCancel := context.WithCancel(ctx)

	// Обновляем контекст в respModel на новый
	respModel.Ctx = sessCtx
	respModel.Cancel = sessCancel

	testChannel := respModel.Chan[treadId]
	if testChannel == nil || !testChannel.IsTxOpen() || !testChannel.IsRxOpen() {
		// Провайдер может вернуть закэшированный, но уже закрытый Ch.
		// При отключении WebSocket переподключение не используется, поэтому
		// для новой сессии создаём новый набор каналов.
		testChannel = &model.Ch{
			TxCh:     make(chan model.Message, create.TxChanBuffer),
			RxCh:     make(chan model.Message, create.RxChanBuffer),
			UserID:   userId,
			DialogID: treadId,
			RespName: "test_responder",
		}
		if respModel.Chan == nil {
			respModel.Chan = make(map[uint64]*model.Ch)
		}
		respModel.Chan[treadId] = testChannel
	}

	logger.Debug("TestAPI: используется канал из respModel, буфер TxCh=%d, адрес=%p",
		cap(testChannel.TxCh), testChannel.TxCh, userId)

	// isDemo уже получен в цикле параллельного ожидания выше

	session := &TestSession{
		UserId:     userId,
		RespId:     respId,
		TreadId:    treadId,
		Channel:    testChannel,
		RespModel:  respModel,
		StartedAt:  time.Now(),
		LastUsedAt: time.Now(),
		Cancel:     sessCancel, // Используем новый cancel из sessCtx
		IsDemo:     isDemo,
	}

	//if isDemo {
	//	logger.Debug("TestAPI: создана сессия для демо-пользователя, лимит=%d запросов/день",
	//		DayLimitForDemoUser, userId)
	//}

	ta.sessions.Store(key, session)
	metrics.ActiveTestSessions.Inc()

	// Создаем StartCh для запуска listener через startpoint
	startData := model.StartCh{
		Model:   respModel,
		Chanel:  testChannel,
		RespId:  respId,
		TreadId: treadId,
		Ctx:     sessCtx, // Используем новый sessCtx
	}

	logger.Debug("TestAPI: подготовка к запуску StarterListener respId=%d, TxCh=%p, RxCh=%p",
		respId, testChannel.TxCh, testChannel.RxCh, userId)

	// Запускаем startpoint.StarterListener
	errCh := make(chan error, 1) // закроется в StarterListener

	ta.starter.StarterListener(startData, errCh)

	// Мониторим ошибки в фоновом режиме
	go func() {
		for {
			select {
			case err := <-errCh:
				if err != nil {
					logger.Error("TestAPI: ошибка от StarterListener, respId=%d: %v", respId, err, userId)
				} else {
					logger.Debug("TestAPI: StarterListener завершил работу без ошибки, respId=%d", respId, userId)
				}
			case <-sessCtx.Done():
				return
			}
		}
	}()

	return session, userModel, nil
}

// GetSessionModel возвращает данные модели для активной сессии
func (ta *TestAPI) GetSessionModel(userId uint32, respId uint64, provider commdom.ProviderType) (*commdom.UniversalModelData, error) {
	key := ta.sessionKey(userId, respId)

	_, ok := ta.sessions.Load(key)
	if !ok {
		return nil, fmt.Errorf("сессия не найдена для userId=%d, respId=%d", userId, respId)
	}

	// Получаем активную модель пользователя
	userModel, err := ta.mod.GetUserModelByProvider(userId, provider)
	if err != nil {
		return nil, fmt.Errorf("ошибка получения активной модели: %w", err)
	}

	return userModel, nil
}

// SendMessage отправляет полное сообщение (с файлами) в активную сессию
func (ta *TestAPI) SendMessage(userId uint32, respId uint64, msg model.Message) error {
	logger.Debug("TestAPI: SendMessage called, respId=%d, message type=%s", respId, msg.Type, userId)
	key := ta.sessionKey(userId, respId)

	session, ok := ta.sessions.Load(key)
	if !ok {
		return fmt.Errorf("сессия не найдена для userId=%d, respId=%d", userId, respId)
	}

	s := session.(*TestSession)
	s.mu.Lock()
	s.LastUsedAt = time.Now()
	s.mu.Unlock()
	return s.Channel.SendToRx(msg)
}

// GetChannel возвращает канал для WebSocket соединения
func (ta *TestAPI) GetChannel(userId uint32, respId uint64) (*model.Ch, error) {
	key := ta.sessionKey(userId, respId)

	session, ok := ta.sessions.Load(key)
	if !ok {
		return nil, fmt.Errorf("сессия не найдена для userId=%d, respId=%d", userId, respId)
	}

	s := session.(*TestSession)
	s.mu.Lock()
	s.LastUsedAt = time.Now()
	s.mu.Unlock()

	return s.Channel, nil
}

// GetAnswer получает ответ из активной сессии с таймаутом (deprecated - используйте GetChannel для WebSocket)
func (ta *TestAPI) GetAnswer(ctx context.Context, userId uint32, respId uint64, timeout time.Duration) (*AnswerResponse, error) {
	key := ta.sessionKey(userId, respId)

	session, ok := ta.sessions.Load(key)
	if !ok {
		return nil, fmt.Errorf("сессия не найдена для userId=%d, respId=%d", userId, respId)
	}

	s := session.(*TestSession)
	s.mu.Lock()
	s.LastUsedAt = time.Now()
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case msg, ok := <-s.Channel.TxCh:
		if !ok {
			return nil, fmt.Errorf("канал ответов закрыт")
		}

		return &AnswerResponse{
			Message:   msg.Content.Message,
			Operator:  msg.Operator.Operator,
			CreatedAt: msg.Timestamp,
		}, nil

	case <-ctx.Done():
		return nil, fmt.Errorf("таймаут ожидания ответа (%s)", timeout)
	}
}

// StopSession останавливает активную сессию
func (ta *TestAPI) StopSession(userId uint32, respId uint64) error {
	key := ta.sessionKey(userId, respId)

	session, ok := ta.sessions.Load(key)
	if !ok {
		// Сессия уже удалена - это нормально (CleanupWebSocketSession мог вызваться раньше)
		logger.Debug("StopSession: сессия уже удалена, respId=%d", respId, userId)
		return nil
	}

	s := session.(*TestSession)
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Cancel != nil {
		s.Cancel()
	}

	// Используем публичный метод Close()
	if s.Channel != nil {
		if err := s.Channel.Close(); err != nil {
			logger.Error("Ошибка закрытия канала в StopSession: %v", err, userId)
		}
	}

	// Удаляем канал из RespModel (Chan - публичное поле)
	if s.RespModel != nil && s.RespModel.Chan != nil {
		delete(s.RespModel.Chan, s.TreadId)
	}

	if _, deleted := ta.sessions.LoadAndDelete(key); deleted {
		metrics.ActiveTestSessions.Dec()
	}

	return nil
}

// ============================================================================
// REALTIME МЕТОДЫ
// ============================================================================

// getRealtimeProvider возвращает RealtimeProvider для заданного userId и respId.
// Приоритет поиска:
//  1. Провайдер из активной TestSession — гарантирует совпадение с тем, которому
//     принадлежит RespModel (хранится в m.responders конкретного провайдера).
//  2. Провайдер с уже активной realtime-сессией для данного respId — для вызовов
//     после успешного StartRealtimeSession, когда TestSession могла быть очищена.
//  3. Активный провайдер пользователя из БД (fallback).
//
// Использование GetRealtimeProvider(userId) напрямую ненадёжно: он запрашивает
// АКТИВНОГО провайдера из БД, который может отличаться от того, с которым создана
// сессия (и где хранится RespModel). Это и было причиной ошибки
// "StartRealtimeSession: RespModel не найден для respId=...".
func (ta *TestAPI) getRealtimeProvider(userId uint32, respId uint64) (model.RealtimeProvider, bool) {
	// 1. Через TestSession (надёжно: тот же провайдер, что использовался в StartSession)
	key := ta.sessionKey(userId, respId)
	if session, ok := ta.sessions.Load(key); ok {
		s := session.(*TestSession)
		if s.RespModel != nil && s.RespModel.Assist.Provider != 0 {
			pmRaw := ta.mod.GetProviderModel(s.RespModel.Assist.Provider)
			if pmRaw != nil {
				if rp, ok := pmRaw.(model.RealtimeProvider); ok {
					return rp, true
				}
			}
		}
	}

	// 2. По наличию активной realtime-сессии у провайдера
	// (для случая когда TestSession уже удалена CleanupWebSocketSession,
	// но realtime-сессия ещё активна — вызовы GetRealtimeChannels, SendRealtimeAudio и т.д.)
	for _, provType := range []commdom.ProviderType{commdom.ProviderOpenAI, commdom.ProviderGoogle, commdom.ProviderMistral} {
		pmRaw := ta.mod.GetProviderModel(provType)
		if pmRaw != nil {
			if rp, ok := pmRaw.(model.RealtimeProvider); ok {
				if rp.GetRealtimeGenerating(respId) != nil {
					return rp, true
				}
			}
		}
	}

	// 3. Fallback: активный провайдер из БД
	return ta.mod.GetRealtimeProvider(userId)
}

// StartRealtimeSession запускает голосовую сессию для активного respId.
// Вызывается из хендлера /ws/test-realtime после upgrade.
// treadId передаётся явно из query-параметра — не зависит от наличия TestSession в sessions
// (сессия могла быть удалена CleanupWebSocketSession после закрытия /ws/test-model).
// Поддерживаются OpenAI Realtime API, Google Live API и Mistral realtime
// cascade (Voxtral STT → Mistral LLM → Voxtral TTS).
func (ta *TestAPI) StartRealtimeSession(userId uint32, respId uint64, treadId uint64) error {
	rp, ok := ta.getRealtimeProvider(userId, respId)
	if !ok {
		return fmt.Errorf("StartRealtimeSession: RealtimeProvider недоступен (модель не поддерживает realtime; поддерживаются OpenAI, Google и Mistral)")
	}

	return rp.StartRealtimeSession(userId, treadId, respId)
}

// StopRealtimeSession завершает голосовую сессию respId.
// Использует DisconnectRealtimeSession который находит провайдер по respId,
// а не по активной модели пользователя — корректно работает для OpenAI и Google.
func (ta *TestAPI) StopRealtimeSession(_ uint32, respId uint64) {
	ta.mod.DisconnectRealtimeSession(respId)
}

// GetRealtimeChannels возвращает каналы аудио и событий для respId.
// Вызывается из хендлера /ws/test-realtime для чтения данных.
// Поддерживаются OpenAI Realtime API, Google Live API и Mistral realtime.
func (ta *TestAPI) GetRealtimeChannels(userId uint32, respId uint64) (<-chan []byte, <-chan model.RealtimeEvent, error) {
	rp, ok := ta.getRealtimeProvider(userId, respId)
	if !ok {
		return nil, nil, fmt.Errorf("GetRealtimeChannels: RealtimeProvider недоступен (поддерживаются OpenAI, Google и Mistral)")
	}

	audioCh, err := rp.GetRealtimeAudio(respId)
	if err != nil {
		return nil, nil, err
	}
	eventCh, err := rp.SubscribeEvents(respId)
	if err != nil {
		return nil, nil, err
	}
	return audioCh, eventCh, nil
}

// UnsubscribeRealtimeEvents отписывает WebSocket-клиента от канала событий сессии.
// Вызывается при закрытии соединения.
func (ta *TestAPI) UnsubscribeRealtimeEvents(userId uint32, respId uint64, sub <-chan model.RealtimeEvent) {
	rp, ok := ta.getRealtimeProvider(userId, respId)
	if !ok {
		return
	}
	rp.UnsubscribeEvents(respId, sub)
}

// SendRealtimeAudio передаёт PCM16-чанк от клиента в голосовую сессию.
func (ta *TestAPI) SendRealtimeAudio(userId uint32, respId uint64, pcm16 []byte) error {
	rp, ok := ta.getRealtimeProvider(userId, respId)
	if !ok {
		return fmt.Errorf("SendRealtimeAudio: RealtimeProvider недоступен")
	}
	return rp.SendRealtimeAudio(respId, pcm16)
}

// CleanupWebSocketSession очищает WebSocket сессию без удаления канала из RespModel
// Используется при закрытии WebSocket соединения для возможности переподключения
// ВАЖНО: НЕ отменяет контекст и НЕ закрывает каналы - они живут до истечения TTL респондента
func (ta *TestAPI) CleanupWebSocketSession(userId uint32, respId uint64) error {
	// Переподключение не поддерживается: отключение WebSocket полностью
	// завершает тестовую сессию. Следующее подключение создаст новые каналы.
	logger.Debug("WebSocket сессия отключена, respId=%d (сессия завершена)", respId, userId)
	return ta.StopSession(userId, respId)
	/*
	   key := ta.sessionKey(userId, respId)

	   session, ok := ta.sessions.Load(key)

	   	if !ok {
	   		// Сессия уже удалена - это нормально (может быть cleanup или повторный вызов)
	   		logger.Debug("CleanupWebSocketSession: сессия уже удалена, respId=%d", respId, userId)
	   		return nil
	   	}

	   s := session.(*TestSession)
	   s.mu.Lock()
	   defer s.mu.Unlock()

	   // ВАЖНО: НЕ отменяем контекст сессии! Он связан с контекстом респондента
	   // Отмена контекста приведет к закрытию каналов, что помешает переподключению
	   // if s.Cancel != nil {
	   //     s.Cancel()  // ← НЕ ВЫЗЫВАЕМ!
	   // }

	   // ВАЖНО: НЕ закрываем канал сессии! Он используется респондентом
	   // if s.Channel != nil {
	   //     if err := s.Channel.Close(); err != nil {
	   //         logger.Warn("Ошибка закрытия канала в CleanupWebSocketSession: %v", err, userId)
	   //     }
	   // }

	   // Не удаляем TestSession сразу: повторное подключение /ws/test-model
	   // должно получить тот же канал через GetChannel. Жизненный цикл сессии
	   // завершит штатный cleanupLoop по TTL LastUsedAt.
	   s.LastUsedAt = time.Now()
	   logger.Debug("WebSocket сессия отключена, respId=%d (сессия и каналы сохранены до TTL)", respId, userId)
	   return nil
	*/
}

// cleanupLoop периодически очищает устаревшие сессии
func (ta *TestAPI) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		ta.mu.RLock()
		now := time.Now()
		ta.mu.RUnlock()

		// Очистка устаревших сессий
		ta.sessions.Range(func(key, value any) bool {
			session := value.(*TestSession)
			session.mu.RLock()
			lastUsed := session.LastUsedAt
			treadId := session.TreadId
			respModel := session.RespModel
			session.mu.RUnlock()

			if now.Sub(lastUsed) > ta.ttl {
				k := key.(string)
				if _, deleted := ta.sessions.LoadAndDelete(k); deleted {
					metrics.ActiveTestSessions.Dec()
				}
				session.Cancel()

				// Используем публичный метод Close()
				if session.Channel != nil {
					if err := session.Channel.Close(); err != nil {
						logger.Error("Ошибка закрытия канала в cleanupLoop: %v (key=%s)", err, k)
					}
				}

				// Удаляем канал из RespModel.Chan (критично для предотвращения утечки памяти!)
				if respModel != nil && respModel.Chan != nil {
					delete(respModel.Chan, treadId)
				}

				logger.Debug("TestAPI: сессия удалена по TTL: %s", k)
			}

			return true
		})
	}
}

// GetSessionsCount возвращает количество активных сессий
func (ta *TestAPI) GetSessionsCount() int {
	count := 0
	ta.sessions.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

// ============================================================================
// ТИПЫ ДЛЯ ОТВЕТОВ
// ============================================================================

// AnswerResponse структура ответа от модели
type AnswerResponse struct {
	Message   string    `json:"message"`
	Operator  bool      `json:"operator,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}
