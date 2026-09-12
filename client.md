# client.md — переход клиентских приложений на новый механизм каналов

Краткая инструкция по миграции приложений, использующих `air-common`.
Касается точки входа сессий (`StartCh` / `StarterListener`), типизации канала
и realtime-режима.

---

## 1. Обновить зависимость

Правки внесены в `air-common`. Для локальной разработки:

```
replace github.com/ikermy/air-common => ../air-common
```

Либо опубликовать `air-common` и обновить (эти изменения вошли в `v1.51.0`):

```
go get github.com/ikermy/air-common@v1.51.0
```

`replace` — только для локальной разработки. После публикации версии его нужно
удалить, иначе сломается сборка в CI/Docker (там `../air-common` недоступен).

---

## 2. Изменения в `model.StartCh`

```go
type StartCh struct {
    Ctx      context.Context
    ChName   comdom.ChannelType // тип канала: было Provider string → Channel → ChName
    Model    *RespModel
    Channel  *Ch                // объект канала: было Chanel *Ch (опечатка исправлена)
    ThreadId uint64             // было TreadId
    RespId   uint64

    Realtime *RealtimeChannels  // NEW: nil = text, non-nil = запрос realtime
}
```

Что сделать:

- `Provider: "telegram"` → `ChName: comdom.Telegram` (и т.п. — см. §4).
- `Chanel:` → `Channel:` (объект канала).
- `TreadId:` → `ThreadId:`.
- Если канал неизвестен — `ChName: comdom.ChannelType(255)` (`"unknown"`),
  тогда в `responderProviders` ничего не пишется.

---

## 3. Точка входа сессий: `StartSession`

**Было** (`StarterListener` удалён):

```go
errCh := make(chan error, 10)
go func() {
    for err := range errCh { /* ... */ }
}()
for start := range telega.GetStartCh() {
    go func(startData model.StartCh) {
        a.Start.StarterListener(startData, errCh)
    }(start)
}
```

**Стало** — `StartSession` возвращает канал ошибок сессии; владелец канала —
сама сессия, она же его закрывает. Общий `errCh` больше не нужен:

```go
for start := range telega.GetStartCh() {
    go func(startData model.StartCh) {
        // ВАЖНО: указатель на копию — StartSession заполняет startData.Realtime
        errCh := a.Start.StartSession(&startData)
        for err := range errCh {
            if err != nil {
                logger.Error("session error: %v", err)
            }
        }
    }(start)
}
```

Правила:
- не закрывать возвращённый канал самостоятельно;
- читать его нужно, иначе не увидите ошибок сессии;
- `StartSession` не блокирует: возвращается после setup, сессия продолжает
  работать.

### Два интерфейса `Start`

Интерфейс на уровне приложения нужен только для запуска текст-сессий и
shutdown, поэтому `CloseSession` в нём не обязателен:

```go
// internal/app
type Start interface {
    StartSession(start *model.StartCh) <-chan error
    Shutdown(shutCh chan<- com.LogMsg)
}
```

Транспорт (бот), который владеет realtime-звонками, объявляет свой узкий
`Start` с `CloseSession` и получает ядро через `SetStart`:

```go
// internal/<transport>
// Start - ядро является единственным
// владельцем lifecycle realtime-сессии: оно запускает провайдера, отдаёт
// каналы аудио/событий через StartCh.Realtime и закрывает сессию по respId.
// Реализуется *startpoint.Start и прокидывается из app.New.
type Start interface {
    StartSession(start *model.StartCh) <-chan error
    CloseSession(respId uint64)
}

func (u *User) SetStart(s Start) { u.start = s }
```

В `app.New` ядро создаётся и прокидывается в транспорт:

```go
s := startpoint.New(ctx, m, e, w, o)
w.SetOperator(o)
w.SetStart(s) // ядро Start -> транспорт
```

В конструкторе бота прокинуть ядро дальше: `bot := &Bot{..., start: u.start}`.
У бота появится поле `start Start`.

---

## 4. `comdom.ChannelType`

```go
const (
    TelegramBot ChannelType = 0
    Web         ChannelType = 1
    Telegram    ChannelType = 2 // String() -> "TelegramUserBot"
    Avito       ChannelType = 3
    Widget      ChannelType = 4
    WhatsApp    ChannelType = 5
    Instagram   ChannelType = 6
)
```

- `String()` возвращает строку (`"TelegramBot"`, `"Web"`, `"TelegramUserBot"`, …);
  для `255` — `"unknown"`.
- `FromString(s)` принимает строку (в т.ч. алиасы), `IsValid()` — валидность.
- `StartCh.ChName.String()` теперь попадает в `responderProviders`, а
  `Start.GetProviderForResponder(respId)` его возвращает. Если раньше вы
  сравнивали возвращённую строку с `"telegram"` / `"whatsapp"` — учтите новое
  значение (`"TelegramUserBot"`, `"WhatsApp"` и т.д.).

---

## 5. Realtime-режим

Ядро (`*startpoint.Start`) — **единственный владелец lifecycle** realtime-сессии:
оно само достаёт `RealtimeProvider` из Router, запускает его и закрывает.
Транспорт больше не вызывает `StartRealtimeSession` / `CloseRealtimeSession`
напрямую; провайдер ему нужен только для `SendRealtimeAudio` (входящее аудио
ядром не канализуется).

Запрос realtime — ненулевой `Realtime` (пустой `&model.RealtimeChannels{}`).
`StartSession` наполняет его каналами до возврата:

```go
startCh := &model.StartCh{
    Ctx:      b.ctx,
    ChName:   comdom.Telegram,
    Model:    respModel, // *model.RespModel; ядро берёт из него Assist.UserID
    Channel:  ch,        // *model.Ch
    ThreadId: dialogID,
    RespId:   respId,
    Realtime: &model.RealtimeChannels{}, // запрос realtime
}

errCh := b.start.StartSession(startCh)
if startCh.Realtime == nil || startCh.Realtime.AudioTx == nil {
    // сессия не поднялась — причину смотреть в errCh
    return fmt.Errorf("realtime-сессия не запущена для respId=%d", respId)
}
// errCh закрывает ядро; читаем в отдельной горутине, чтобы не потерять ошибки.
go func() {
    for err := range errCh {
        if err != nil {
            logger.Warn("Realtime-сессия respId=%d: %v", respId, err)
        }
    }
}()

// Каналы сессии:
audioOut := startCh.Realtime.AudioTx // <-chan []byte : ответ провайдера
drain    := startCh.Realtime.Drain   // <-chan struct{} : сбросить playback
events   := startCh.Realtime.Events  // <-chan model.RealtimeEvent : события/ошибки
```

- **Model обязателен**: `StartSession` требует `start.Model != nil`
  (`*model.RespModel`). Если канал пережил рестарт, а модель в памяти
  потеряна — восстановить её через `mod.GetOrSetRespGPT(...)`.
- **Аудио на вход** — процедурное: `rp.SendRealtimeAudio(respId, pcm16)`,
  где `rp` берётся из Router (`GetRealtimeProvider(userID)`).
- **Ошибки** приходят и в `events` (`Type == "error"`), и в `errCh`.
- **Завершение** — `b.start.CloseSession(respId)` (например, в cleanup звонка).
  После этого `errCh` закрывается, реестр сессии очищается.
- `Realtime == nil` → text-режим (`ChName`/`Channel`).

Подписки на события создаёт ядро (`runRealtimeSession` делает две независимые
подписки: транспорту и мосту ошибок), транспорт ничего не подписывает и не
отписывает сам.

Важно: realtime-сессии вызывают `StartSession` напрямую и **не** идут через
общий `whatsapp.StartCh` / `App.Starter()` (тот обслуживает только text-сессии).
Поэтому `errCh` realtime-сессии нужно вычитывать самому — иначе после первой
ошибки писатель ядра заблокируется и ошибки потеряются.

---

## 6. Чек-лист

- [ ] Обновлена зависимость `air-common` (`replace` — для локалки, убрать
      после публикации версии).
- [ ] `Provider: "..."` → `ChName: comdom.<...>`.
- [ ] `Chanel:` → `Channel:`.
- [ ] `TreadId:` → `ThreadId:`.
- [ ] `StarterListener(start, errCh)` → `errCh := StartSession(&start)`.
- [ ] Общий `errCh` удалён; канал каждой сессии вычитывается до закрытия.
- [ ] В транспорте объявлен свой `Start` (`StartSession` + `CloseSession`),
      ядро прокинуто через `SetStart` в `app.New` и поле `start` у бота.
- [ ] Realtime: запрос через `Realtime: &model.RealtimeChannels{}`, проверка
      `Realtime.AudioTx != nil`, чтение `Realtime.AudioTx/Drain/Events`,
      ошибки из `errCh`.
- [ ] Lifecycle реальтайма не трогается напрямую: `StartRealtimeSession` /
      `CloseRealtimeSession` заменены на `StartSession` / `CloseSession`,
      провайдер остаётся только для `SendRealtimeAudio`.
- [ ] `start.Model` (`*model.RespModel`) заполнен; после рестарта модель
      восстанавливается (`GetOrSetRespGPT`).
- [ ] Проверены потребители `GetProviderForResponder` (значения `String()`).
- [ ] `go build ./...` и `go vet ./...` зелёные.

---

## 7. Изменения API (breaking)

| Было | Стало |
|---|---|
| `Start.StarterListener(start model.StartCh, errCh chan<- error)` | `Start.StartSession(start *model.StartCh) <-chan error` |
| `StartCh.Provider string` | `StartCh.ChName comdom.ChannelType` |
| `StartCh.Chanel *Ch` | `StartCh.Channel *Ch` |
| `StartCh.TreadId uint64` | `StartCh.ThreadId uint64` |
| `comdom.ChatType` | `comdom.ChannelType` |
| прямое управление `RealtimeProvider` (`StartRealtimeSession` / `GetRealtimeAudio` / `SubscribeEvents` / `CloseRealtimeSession`) из транспорта | ядро `StartSession` + `StartCh.Realtime.AudioTx/Drain/Events` + `CloseSession`; провайдер — только `SendRealtimeAudio` |
| нет точки передачи ядра в транспорт | `User.SetStart(core Start)` + поле `start` у бота |
