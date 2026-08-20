-- --------------------------------------------------------
-- Хост:                         127.0.0.1
-- Версия сервера:               13.0.1-MariaDB-ubu2604 - mariadb.org binary distribution
-- Операционная система:         debian-linux-gnu
-- HeidiSQL Версия:              12.11.0.7065
-- --------------------------------------------------------

/*!40101 SET @OLD_CHARACTER_SET_CLIENT=@@CHARACTER_SET_CLIENT */;
/*!40101 SET NAMES utf8 */;
/*!50503 SET NAMES utf8mb4 */;
/*!40103 SET @OLD_TIME_ZONE=@@TIME_ZONE */;
/*!40103 SET TIME_ZONE='+00:00' */;
/*!40014 SET @OLD_FOREIGN_KEY_CHECKS=@@FOREIGN_KEY_CHECKS, FOREIGN_KEY_CHECKS=0 */;
/*!40101 SET @OLD_SQL_MODE=@@SQL_MODE, SQL_MODE='NO_AUTO_VALUE_ON_ZERO' */;
/*!40111 SET @OLD_SQL_NOTES=@@SQL_NOTES, SQL_NOTES=0 */;


-- Дамп структуры базы данных air
CREATE DATABASE IF NOT EXISTS `air` /*!40100 DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci */;
USE `air`;

-- Дамп структуры для таблица air.app_config
CREATE TABLE IF NOT EXISTS `app_config` (
  `key` varchar(100) NOT NULL,
  `value` text NOT NULL,
  `updated_at` timestamp NOT NULL DEFAULT current_timestamp() ON UPDATE current_timestamp(),
  PRIMARY KEY (`key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.billing
CREATE TABLE IF NOT EXISTS `billing` (
  `Id` bigint(20) NOT NULL AUTO_INCREMENT,
  `Date` timestamp NOT NULL DEFAULT current_timestamp(),
  `UserId` int(11) NOT NULL,
  `Payment` decimal(10,2) NOT NULL DEFAULT 0.00,
  `Currency` tinyint(4) NOT NULL DEFAULT 1,
  `Months` tinyint(4) NOT NULL DEFAULT 1,
  `Discont` decimal(10,2) NOT NULL DEFAULT 0.00,
  PRIMARY KEY (`Id`),
  KEY `FK_billing_users` (`UserId`),
  KEY `FK_billing_currency` (`Currency`),
  CONSTRAINT `FK_billing_currency` FOREIGN KEY (`Currency`) REFERENCES `currency` (`Id`),
  CONSTRAINT `FK_billing_users` FOREIGN KEY (`UserId`) REFERENCES `users` (`Id`)
) ENGINE=InnoDB AUTO_INCREMENT=23 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.channels
CREATE TABLE IF NOT EXISTS `channels` (
  `UserId` int(11) NOT NULL,
  `TgBot` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin DEFAULT NULL,
  `TgBot_enabled` tinyint(4) NOT NULL DEFAULT 0,
  `Widget` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin DEFAULT NULL,
  `Widget_enabled` tinyint(4) NOT NULL DEFAULT 0,
  `TgUserBot` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin DEFAULT NULL,
  `TgUserBot_enabled` tinyint(4) NOT NULL DEFAULT 0,
  `Whats` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin DEFAULT NULL,
  `Whats_enabled` tinyint(4) NOT NULL DEFAULT 0,
  `Insta` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin DEFAULT NULL,
  `Insta_enabled` tinyint(4) NOT NULL DEFAULT 0,
  `Avito` longtext DEFAULT NULL,
  `Avito_enabled` tinyint(1) NOT NULL DEFAULT 0,
  PRIMARY KEY (`UserId`),
  CONSTRAINT `FK_channels_users` FOREIGN KEY (`UserId`) REFERENCES `users` (`Id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.chat_type
CREATE TABLE IF NOT EXISTS `chat_type` (
  `Id` tinyint(4) NOT NULL DEFAULT 0,
  `Name` varchar(20) NOT NULL,
  PRIMARY KEY (`Id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.crm_configs
CREATE TABLE IF NOT EXISTS `crm_configs` (
  `id` int(11) unsigned NOT NULL AUTO_INCREMENT,
  `user_id` int(11) NOT NULL,
  `crm_type` varchar(50) NOT NULL,
  `name` varchar(255) NOT NULL,
  `subdomain` varchar(255) DEFAULT NULL,
  `credentials` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `options` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL COMMENT 'ID поля "Источник перехода" для автоматического заполнения MarusiaAI',
  `channels` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin DEFAULT NULL,
  `is_active` tinyint(1) DEFAULT 1,
  `created_at` datetime(3) DEFAULT current_timestamp(3),
  `updated_at` datetime(3) DEFAULT current_timestamp(3) ON UPDATE current_timestamp(3),
  PRIMARY KEY (`id`) USING BTREE,
  UNIQUE KEY `unique_user_crm_type` (`user_id`,`crm_type`) USING BTREE COMMENT 'Один пользователь может иметь только одну конфигурацию каждого типа CRM',
  KEY `idx_user_id` (`user_id`) USING BTREE,
  CONSTRAINT `FK_crm_configs_users` FOREIGN KEY (`user_id`) REFERENCES `users` (`Id`) ON DELETE CASCADE
) ENGINE=InnoDB AUTO_INCREMENT=71 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Конфигурации CRM для пользователей. У одного пользователя может быть только одна конфигурация для каждого типа CRM.';

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.crm_mappings
CREATE TABLE IF NOT EXISTS `crm_mappings` (
  `id` int(11) unsigned NOT NULL AUTO_INCREMENT,
  `user_id` int(11) NOT NULL,
  `application_id` int(10) unsigned NOT NULL,
  `crm_config_id` int(10) unsigned NOT NULL,
  `field_mapping` text DEFAULT NULL,
  `pipeline_id` varchar(100) DEFAULT NULL,
  `status_id` varchar(100) DEFAULT NULL,
  `responsible_user_id` varchar(100) DEFAULT NULL,
  `is_active` tinyint(1) DEFAULT 1,
  `priority` bigint(20) DEFAULT 0,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `unique_app_crm` (`application_id`,`crm_config_id`),
  KEY `idx_user_id` (`user_id`),
  KEY `idx_app_id` (`user_id`,`application_id`),
  KEY `fk_crm_mappings_crm_config` (`crm_config_id`),
  CONSTRAINT `FK_crm_mappings_users` FOREIGN KEY (`user_id`) REFERENCES `users` (`Id`) ON DELETE CASCADE,
  CONSTRAINT `crm_mappings_ibfk_1` FOREIGN KEY (`application_id`) REFERENCES `crm_applications` (`id`) ON DELETE CASCADE,
  CONSTRAINT `crm_mappings_ibfk_2` FOREIGN KEY (`crm_config_id`) REFERENCES `crm_configs` (`id`) ON DELETE CASCADE,
  CONSTRAINT `fk_crm_mappings_application` FOREIGN KEY (`application_id`) REFERENCES `crm_applications` (`id`) ON DELETE CASCADE,
  CONSTRAINT `fk_crm_mappings_crm_config` FOREIGN KEY (`crm_config_id`) REFERENCES `crm_configs` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Маппинги приложений на CRM';

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.crm_oauth_states
CREATE TABLE IF NOT EXISTS `crm_oauth_states` (
  `state` varchar(255) NOT NULL COMMENT 'Уникальный идентификатор OAuth state (hex 32 символа)',
  `user_id` int(11) NOT NULL COMMENT 'ID пользователя',
  `client_id` varchar(255) NOT NULL COMMENT 'OAuth Client ID',
  `client_secret` varchar(255) NOT NULL COMMENT 'OAuth Client Secret',
  `redirect_url` varchar(255) NOT NULL COMMENT 'URL для callback',
  `subdomain` varchar(255) NOT NULL COMMENT 'Subdomain CRM',
  `crm_type` varchar(50) NOT NULL COMMENT 'Тип CRM (amocrm, bitrix24, ...)',
  `created_at` timestamp NULL DEFAULT current_timestamp() COMMENT 'Время создания',
  `expires_at` timestamp NOT NULL DEFAULT current_timestamp() ON UPDATE current_timestamp() COMMENT 'Время истечения (TTL 10 минут)',
  PRIMARY KEY (`state`),
  KEY `idx_expires` (`expires_at`) COMMENT 'Индекс для быстрой очистки истекших',
  KEY `FK_crm_oauth_states_users` (`user_id`),
  CONSTRAINT `FK_crm_oauth_states_users` FOREIGN KEY (`user_id`) REFERENCES `users` (`Id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Временное хранилище OAuth states для защиты от CSRF';

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.crypto_payments
CREATE TABLE IF NOT EXISTS `crypto_payments` (
  `id` int(11) NOT NULL AUTO_INCREMENT,
  `order_id` varchar(64) NOT NULL,
  `user_id` int(11) NOT NULL,
  `currency` varchar(10) NOT NULL,
  `network` varchar(20) NOT NULL,
  `amount` decimal(20,8) NOT NULL,
  `amount_usd` decimal(10,2) NOT NULL,
  `deposit_address` varchar(255) NOT NULL,
  `deposit_tag` varchar(100) DEFAULT NULL,
  `received_amount` decimal(20,8) DEFAULT 0.00000000,
  `tx_hash` varchar(255) DEFAULT NULL,
  `confirmations` int(11) DEFAULT 0,
  `status` enum('pending','partial','confirmed','failed','expired') DEFAULT 'pending',
  `expires_at` datetime NOT NULL,
  `created_at` datetime DEFAULT current_timestamp(),
  `updated_at` datetime DEFAULT current_timestamp() ON UPDATE current_timestamp(),
  PRIMARY KEY (`id`),
  UNIQUE KEY `order_id` (`order_id`),
  KEY `idx_order_id` (`order_id`),
  KEY `idx_user_id` (`user_id`),
  KEY `idx_status` (`status`),
  KEY `idx_expires_at` (`expires_at`),
  KEY `idx_created_at` (`created_at`),
  KEY `idx_crypto_payments_status_expires` (`status`,`expires_at`),
  KEY `idx_crypto_payments_address` (`deposit_address`),
  KEY `idx_active_payments_by_address` (`user_id`,`currency`,`network`,`deposit_address`,`status`,`expires_at`),
  KEY `idx_user_active_payments` (`user_id`,`currency`,`network`,`status`,`expires_at`,`created_at`),
  CONSTRAINT `fk_crypto_payments_user` FOREIGN KEY (`user_id`) REFERENCES `users` (`Id`) ON DELETE CASCADE
) ENGINE=InnoDB AUTO_INCREMENT=9 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.currency
CREATE TABLE IF NOT EXISTS `currency` (
  `Id` tinyint(4) NOT NULL AUTO_INCREMENT,
  `Name` varchar(4) NOT NULL,
  `MonthCost` decimal(10,2) NOT NULL DEFAULT 0.00,
  `Discont` decimal(10,2) NOT NULL DEFAULT 0.00,
  `Discounts` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin DEFAULT NULL CHECK (json_valid(`Discounts`)),
  PRIMARY KEY (`Id`)
) ENGINE=InnoDB AUTO_INCREMENT=6 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для процедура air.DeleteAllUserData
DELIMITER //
CREATE PROCEDURE `DeleteAllUserData`(
	IN `p_UserId` INT
)
    COMMENT 'Полное удаление пользователя и всех связанных данных'
BEGIN
    DECLARE EXIT HANDLER FOR SQLEXCEPTION
    BEGIN
        ROLLBACK;
        RESIGNAL;
    END;

    START TRANSACTION;

    -- Удаляем криптоплатежи
    DELETE FROM crypto_payments WHERE user_id = p_UserId;

    -- Удаляем данные биллинга
    DELETE FROM billing WHERE UserId = p_UserId;

    -- Удаляем подписки
    DELETE FROM subscriptions WHERE UserId = p_UserId;

    -- Удаляем уведомления
    DELETE FROM notifications WHERE UserId = p_UserId;

    -- Удаляем каналы
    DELETE FROM channels WHERE UserId = p_UserId;

    -- Удаляем диалоги пользователя
    DELETE FROM dialogs WHERE User = p_UserId;

    -- Удаляем данные авторизации
    DELETE FROM user_auth WHERE UserId = p_UserId;

    -- Удаляем все данные, добавленные после первоначальной версии процедуры
    DELETE FROM user_api_keys WHERE UserId = p_UserId;
    DELETE FROM google_oauth_tokens WHERE user_id = p_UserId;
    DELETE FROM vector_embeddings WHERE user_id = p_UserId;
    DELETE FROM crm_oauth_states WHERE user_id = p_UserId;
    DELETE FROM crm_configs WHERE user_id = p_UserId;
    DELETE FROM user_storage_config WHERE user_id = p_UserId;
    DELETE FROM user_storage_quota WHERE user_id = p_UserId;
    DELETE FROM storage_migrations WHERE user_id = p_UserId;

    -- Данные lead-сервиса и его зависимые записи
    DELETE FROM service_contacts WHERE UserId = p_UserId;
    DELETE FROM service_gpt WHERE UserId = p_UserId;
    DELETE FROM service_proxy WHERE UserId = p_UserId;
    DELETE FROM service_tgbots WHERE UserId = p_UserId;
    DELETE FROM service_wabots WHERE UserId = p_UserId;
    DELETE FROM service WHERE UserId = p_UserId;

    -- Удаляем операторов (важно: FK RESTRICT → нужно явно)
    DELETE FROM operators WHERE UserId = p_UserId;

    -- Удаляем модели пользователя через новую схему user_models
    -- Сначала получаем ID всех моделей пользователя
    DELETE FROM user_gpt
    WHERE Id IN (
        SELECT ModelId FROM user_models WHERE UserId = p_UserId
    );

    -- Удаляем связи пользователь-модель (если CASCADE не сработал)
    DELETE FROM user_models WHERE UserId = p_UserId;

    -- Удаляем самого пользователя последним
    DELETE FROM users WHERE Id = p_UserId;
    
    COMMIT;
END//
DELIMITER ;

-- Дамп структуры для процедура air.DeleteDialog
DELIMITER //
CREATE PROCEDURE `DeleteDialog`(
    IN `p_DialogId` BIGINT,
    IN `p_UserId` INT
)
BEGIN
    DECLARE v_RoleId TINYINT;
    
    -- Получаем RoleId пользователя
    SELECT RoleId INTO v_RoleId
    FROM users
    WHERE Id = p_UserId;
    
    -- Проверяем, является ли пользователь демо (RoleId = 1)
    IF v_RoleId = 1 THEN
        -- Возвращаем специальную ошибку с кодом SQLSTATE '45001'
        SIGNAL SQLSTATE '45001'
        SET MESSAGE_TEXT = 'Невозможно удалить диалог демо пользователя';
    END IF;
    
    -- Если проверка пройдена, удаляем диалог
    DELETE FROM dialogs
    WHERE Id = p_DialogId;
END//
DELIMITER ;

-- Экспортируемые данные не выделены.

-- Экспортируемые данные не выделены.

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.dialogs
CREATE TABLE IF NOT EXISTS `dialogs` (
  `Id` bigint(20) NOT NULL AUTO_INCREMENT,
  `Date` timestamp NOT NULL DEFAULT current_timestamp(),
  `Type` tinyint(4) NOT NULL DEFAULT 0,
  `User` int(11) NOT NULL COMMENT 'Какому пользователю принадлежит диалог',
  `Responder` bigint(20) NOT NULL COMMENT 'Кто общается с ассистентом',
  `Data` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin DEFAULT NULL,
  `Context` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin DEFAULT NULL CHECK (json_valid(`Context`)),
  `Target` int(1) NOT NULL DEFAULT 0,
  `Trigger` int(1) NOT NULL DEFAULT 0,
  PRIMARY KEY (`Id`),
  KEY `fk_user_id` (`User`) USING BTREE,
  KEY `fk_chat_type` (`Type`),
  KEY `fk_responder_id` (`Responder`),
  CONSTRAINT `fk_chat_type` FOREIGN KEY (`Type`) REFERENCES `chat_type` (`Id`),
  CONSTRAINT `fk_responder_id` FOREIGN KEY (`Responder`) REFERENCES `responders` (`Id`) ON DELETE CASCADE ON UPDATE CASCADE,
  CONSTRAINT `fk_user_id` FOREIGN KEY (`User`) REFERENCES `users` (`Id`)
) ENGINE=InnoDB AUTO_INCREMENT=283 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для процедура air.DisableAllUserChannel
DELIMITER //
CREATE PROCEDURE `DisableAllUserChannel`(
	IN `p_UserId` INT
)
BEGIN
    DECLARE sql_query TEXT;

    -- Проверка наличия пользователя в таблице channels
    IF NOT EXISTS (
        SELECT 1
        FROM channels
        WHERE UserId = p_UserId
    ) THEN
        SIGNAL SQLSTATE '45000'
        SET MESSAGE_TEXT = 'Пользователь не найден в таблице channels!';
    END IF;

    -- Формирование SQL-запроса для обновления всех столбцов, содержащих "enabled" в названии
    SELECT GROUP_CONCAT(CONCAT('`', COLUMN_NAME, '` = 0') SEPARATOR ', ')
    INTO sql_query
    FROM INFORMATION_SCHEMA.COLUMNS
    WHERE TABLE_NAME = 'channels'
      AND COLUMN_NAME LIKE '%enabled%';

    -- Выполнение динамического SQL-запроса
    SET @dynamic_query = CONCAT(
        'UPDATE channels SET ', sql_query, ' WHERE UserId = ', p_UserId
    );

    PREPARE stmt FROM @dynamic_query;
    EXECUTE stmt;
    DEALLOCATE PREPARE stmt;
END//
DELIMITER ;

-- Дамп структуры для функция air.GetBalance
DELIMITER //
CREATE FUNCTION `GetBalance`(`userId` INT
) RETURNS decimal(10,2)
BEGIN
    DECLARE userBalance DECIMAL(10,2);

    SELECT `balance`
    INTO userBalance
    FROM `users`
    WHERE `Id` = userId;

    RETURN userBalance;
END//
DELIMITER ;

-- Дамп структуры для функция air.GetNotificationChannel
DELIMITER //
CREATE FUNCTION `GetNotificationChannel`(`in_UserId` INT
) RETURNS longtext CHARSET utf8mb4 COLLATE utf8mb4_bin
BEGIN
    DECLARE channel_info JSON DEFAULT JSON_ARRAY();  -- Инициализация пустого массива
    DECLARE email_val VARCHAR(255);
    DECLARE telegram_val VARCHAR(255);

    -- Проверка и получение Email
    IF EXISTS (
        SELECT 1 FROM notifications WHERE UserId = in_UserId AND Email = 1
    ) THEN
        SELECT ua.Email INTO email_val
        FROM user_auth ua
        WHERE ua.UserId = in_UserId AND Confirmed = 1 AND Disabled = 0;

        IF email_val IS NULL THEN
            SET email_val = 'null';
        END IF;

        SET channel_info = JSON_ARRAY_APPEND(
            channel_info, '$',
            JSON_OBJECT('channel_type', 'mail', 'channel_value', email_val)
        );
    END IF;

    -- Проверка и получение Telegram
    IF EXISTS (
        SELECT 1 FROM notifications WHERE UserId = in_UserId AND Telegram_enabled = 1
    ) THEN
        SELECT CAST(Telegram AS CHAR) INTO telegram_val
        FROM notifications
        WHERE UserId = in_UserId;

        IF telegram_val IS NULL OR telegram_val = '0' THEN
            SET telegram_val = 'null';
        END IF;

        SET channel_info = JSON_ARRAY_APPEND(
            channel_info, '$',
            JSON_OBJECT('channel_type', 'telegram', 'channel_value', telegram_val)
        );
    END IF;

    -- Проверка Instant
    IF EXISTS (
        SELECT 1 FROM notifications WHERE UserId = in_UserId AND Instant = 1
    ) THEN
        SET channel_info = JSON_ARRAY_APPEND(
            channel_info, '$',
            JSON_OBJECT('channel_type', 'instant', 'channel_value', '1')
        );
    END IF;

    RETURN channel_info;
END//
DELIMITER ;

-- Дамп структуры для функция air.GetOrSetDialog
DELIMITER //
CREATE FUNCTION `GetOrSetDialog`(`p_Type` INT,
	`p_UserId` INT,
	`p_Responder` BIGINT
) RETURNS bigint(20)
BEGIN
    DECLARE dialogId BIGINT;

    -- Проверка на наличие диалога с указанными параметрами
    SELECT `Id` INTO dialogId
    FROM `dialogs`
    WHERE `Type` = p_Type AND `User` = p_UserId AND `Responder` = p_Responder
    LIMIT 1;

    -- Если диалог найден, возвращаем его Id
    IF dialogId IS NOT NULL THEN
        RETURN dialogId;
    ELSE
        -- Если диалог не найден, создаем новый диалог
        INSERT INTO `dialogs` (`Type`, `User`, `Responder`)
        VALUES (p_Type, p_UserId, p_Responder);

        -- Возвращаем Id нового диалога
        RETURN LAST_INSERT_ID();
    END IF;
END//
DELIMITER ;

-- Дамп структуры для процедура air.GetOrSetTreadAndResponder
DELIMITER //
CREATE PROCEDURE `GetOrSetTreadAndResponder`(
	IN `userId` INT,
	IN `responderRealId` BIGINT,
	IN `responderName` VARCHAR(120),
	IN `p_type` TINYINT,
	OUT `out_dialogId` BIGINT
)
    COMMENT 'Возвращает ID  диалога или создает новый а так же нового респондера'
BEGIN
    DECLARE dialogId BIGINT;
    DECLARE responderId BIGINT;
    DECLARE currentName VARCHAR(120);

    -- Попытка найти существующий диалог
    SELECT `Id`
    INTO dialogId
    FROM `dialogs`
    WHERE `User` = userId AND `Responder` = (
        SELECT `Id`
        FROM `responders`
        WHERE `RealId` = responderRealId
    ) AND `Type` = p_type
    LIMIT 1;

    -- Проверяем, существует ли Responder
    SELECT `Id`, `Name`
    INTO responderId, currentName
    FROM `responders`
    WHERE `RealId` = responderRealId;

    -- Если Responder не найден, создаем нового
    IF responderId IS NULL THEN
        INSERT INTO `responders` (`Name`, `Type`, `RealId`, `Date`)
        VALUES (responderName, p_type, responderRealId, CURRENT_TIMESTAMP());
        SET responderId = LAST_INSERT_ID();
    ELSE
        -- Обновляем имя, если оно пустое или отличается от нового
        IF responderName IS NOT NULL AND (currentName IS NULL OR currentName = '' OR responderName != currentName) THEN
            UPDATE `responders`
            SET `Name` = responderName
            WHERE `Id` = responderId;
        END IF;
    END IF;

    -- Если диалог найден, возвращаем его ID
    IF dialogId IS NOT NULL THEN
        SET out_dialogId = dialogId;
    ELSE
        -- Создание нового диалога
        INSERT INTO `dialogs` (`User`, `Responder`, `Type`, `Date`)
        VALUES (userId, responderId, p_type, CURRENT_TIMESTAMP());
        SET out_dialogId = LAST_INSERT_ID();
    END IF;

    -- Возвращаем результат
    SELECT out_dialogId;
END//
DELIMITER ;

-- Дамп структуры для функция air.GetUserSubscriptionLimites
DELIMITER //
CREATE FUNCTION `GetUserSubscriptionLimites`(`in_userId` INT
) RETURNS longtext CHARSET utf8mb4 COLLATE utf8mb4_bin
BEGIN
    DECLARE result JSON;
    
    SELECT 
        JSON_OBJECT(
            'balance', u.balance,
            'EndDate', s.EndDate
        )
    INTO result
    FROM 
        users AS u
    JOIN 
        subscriptions AS s ON u.Id = s.UserId  
    WHERE 
        u.Id = in_userId;
    
    RETURN result;
END//
DELIMITER ;

-- Дамп структуры для функция air.GetUserTariff
DELIMITER //
CREATE FUNCTION `GetUserTariff`(`UserId` INT
) RETURNS longtext CHARSET utf8mb4 COLLATE utf8mb4_bin
BEGIN
    DECLARE tariffData JSON;
    DECLARE v_roleId INT;

    -- Определяем роль пользователя
    SELECT RoleId INTO v_roleId
    FROM users
    WHERE Id = UserId;

    IF v_roleId = 3 THEN
        -- Для роли 3 берём данные из service_currency
        SELECT JSON_OBJECT(
            'Currency', sc.Name,
            'MonthCost', sc.MonthCost,
            'Discounts', JSON_EXTRACT(sc.Discounts, '$'),
            'Messages', sc.Messages,
            'EndDate', s.EndDate,
            'Billing', JSON_ARRAYAGG(
                JSON_OBJECT(
                    'Date', b.Date,
                    'Payment', b.Payment,
                    'Months', b.Months,
                    'Discount', b.Discont
                )
            )
        )
        INTO tariffData
        FROM users u
        INNER JOIN service_currency sc ON u.currency = sc.Id
        LEFT JOIN subscriptions s ON s.UserId = UserId
        LEFT JOIN billing b ON b.UserId = UserId
        WHERE u.Id = UserId;

    ELSE
        -- Для всех остальных ролей — стандартная currency
        SELECT JSON_OBJECT(
            'Currency', c.Name,
            'MonthCost', c.MonthCost,
            'Discounts', c.Discounts,
            'EndDate', s.EndDate,
            'Billing', JSON_ARRAYAGG(
                JSON_OBJECT(
                    'Date', b.Date,
                    'Payment', b.Payment,
                    'Months', b.Months,
                    'Discount', b.Discont
                )
            )
        )
        INTO tariffData
        FROM users u
        INNER JOIN currency c ON u.currency = c.Id
        LEFT JOIN subscriptions s ON s.UserId = UserId
        LEFT JOIN billing b ON b.UserId = UserId
        WHERE u.Id = UserId;
    END IF;

    RETURN tariffData;
END//
DELIMITER ;

-- Дамп структуры для таблица air.google_oauth_tokens
CREATE TABLE IF NOT EXISTS `google_oauth_tokens` (
  `id` int(10) unsigned NOT NULL AUTO_INCREMENT,
  `user_id` int(10) unsigned NOT NULL,
  `google_email` varchar(255) NOT NULL DEFAULT '',
  `access_token` text NOT NULL,
  `refresh_token` varchar(512) NOT NULL DEFAULT '',
  `token_type` enum('Bearer') NOT NULL DEFAULT 'Bearer',
  `expiry` datetime NOT NULL,
  `scopes` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin DEFAULT NULL CHECK (json_valid(`scopes`)),
  `created_at` datetime NOT NULL DEFAULT current_timestamp(),
  `updated_at` datetime NOT NULL DEFAULT current_timestamp() ON UPDATE current_timestamp(),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_user` (`user_id`)
) ENGINE=InnoDB AUTO_INCREMENT=133 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.gpt_models
CREATE TABLE IF NOT EXISTS `gpt_models` (
  `Id` int(11) NOT NULL AUTO_INCREMENT,
  `Provider` tinyint(4) NOT NULL DEFAULT 1,
  `IsDefault` tinyint(1) NOT NULL DEFAULT 0,
  `Name` tinytext NOT NULL,
  PRIMARY KEY (`Id`),
  KEY `FK_provider` (`Provider`),
  CONSTRAINT `FK_provider` FOREIGN KEY (`Provider`) REFERENCES `model_providers` (`Id`) ON DELETE NO ACTION ON UPDATE NO ACTION
) ENGINE=InnoDB AUTO_INCREMENT=1119 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.languages
CREATE TABLE IF NOT EXISTS `languages` (
  `Id` tinyint(4) NOT NULL AUTO_INCREMENT,
  `Code` varchar(2) NOT NULL,
  PRIMARY KEY (`Id`)
) ENGINE=InnoDB AUTO_INCREMENT=4 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.model_providers
CREATE TABLE IF NOT EXISTS `model_providers` (
  `Id` tinyint(4) NOT NULL AUTO_INCREMENT,
  `Name` varchar(50) NOT NULL,
  `Code` varchar(20) NOT NULL,
  `IsActive` tinyint(1) NOT NULL DEFAULT 1,
  `CreatedAt` timestamp NOT NULL DEFAULT current_timestamp(),
  PRIMARY KEY (`Id`),
  UNIQUE KEY `uq_code` (`Code`)
) ENGINE=InnoDB AUTO_INCREMENT=4 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.notifications
CREATE TABLE IF NOT EXISTS `notifications` (
  `UserId` int(11) NOT NULL DEFAULT 0,
  `Email` tinyint(1) DEFAULT 0,
  `Telegram` bigint(20) DEFAULT NULL,
  `Telegram_enabled` tinyint(1) NOT NULL DEFAULT 0,
  `Instant` tinyint(1) NOT NULL DEFAULT 0,
  `Start` tinyint(1) NOT NULL DEFAULT 0,
  `End` tinyint(1) NOT NULL DEFAULT 0,
  `Target` tinyint(1) NOT NULL DEFAULT 0,
  PRIMARY KEY (`UserId`) USING BTREE,
  KEY `FK__users` (`UserId`),
  CONSTRAINT `FK__users` FOREIGN KEY (`UserId`) REFERENCES `users` (`Id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.operators
CREATE TABLE IF NOT EXISTS `operators` (
  `Id` bigint(20) NOT NULL AUTO_INCREMENT,
  `UserId` int(11) NOT NULL DEFAULT 0,
  `Changed` tinyint(1) NOT NULL DEFAULT 0,
  `Timechange` timestamp NOT NULL DEFAULT current_timestamp(),
  `Telegram` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin DEFAULT NULL CHECK (json_valid(`Telegram`)),
  `Telegram_enabled` tinyint(1) NOT NULL DEFAULT 0,
  `Widget` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin DEFAULT NULL CHECK (json_valid(`Widget`)),
  `Widget_enabled` tinyint(1) NOT NULL DEFAULT 0,
  PRIMARY KEY (`Id`),
  UNIQUE KEY `UserId` (`UserId`),
  CONSTRAINT `FK_userId` FOREIGN KEY (`UserId`) REFERENCES `users` (`Id`) ON DELETE CASCADE ON UPDATE CASCADE
) ENGINE=InnoDB AUTO_INCREMENT=24 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для функция air.ReadContext
DELIMITER //
CREATE FUNCTION `ReadContext`(`p_DialogId` BIGINT,
	`p_Provider` ENUM('openai', 'mistral')
) RETURNS longtext CHARSET utf8mb4 COLLATE utf8mb4_bin
    COMMENT 'Возвращает сохранённый контекст для указанного диалога'
BEGIN
    DECLARE contextData JSON;
    DECLARE providerData JSON;

    -- Извлекаем поле Context из таблицы dialogs
    SELECT `Context`
    INTO contextData
    FROM `dialogs`
    WHERE `Id` = p_DialogId;

    -- Извлекаем данные конкретного провайдера
    IF contextData IS NULL THEN
        RETURN NULL;
    END IF;

    SET providerData = JSON_EXTRACT(contextData, CONCAT('$.', p_Provider));

    -- Возвращаем данные провайдера или NULL если их нет
    RETURN providerData;
END//
DELIMITER ;

-- Дамп структуры для функция air.ReadDialog
DELIMITER //
CREATE FUNCTION `ReadDialog`(p_DialogId BIGINT, p_Limit INT) RETURNS longtext CHARSET utf8mb4 COLLATE utf8mb4_bin
    COMMENT 'Получает данные диалога с именем активной модели пользователя'
BEGIN
    DECLARE dialogData JSON;
    DECLARE userName VARCHAR(255);
    DECLARE responderName VARCHAR(255);

    -- Получаем данные из таблицы dialogs (с учетом лимита)
    IF p_Limit IS NULL THEN
        -- Берём самое последнее сообщение
        SELECT Data, Type, User, Responder, Date
        INTO @data, @type, @user, @responder, @date
        FROM dialogs
        WHERE Id = p_DialogId
        ORDER BY Date DESC
        LIMIT 1;
    ELSE
        -- Берём последнее сообщение из последних p_Limit
        SELECT Data, Type, User, Responder, Date
        INTO @data, @type, @user, @responder, @date
        FROM dialogs
        WHERE Id = p_DialogId
        ORDER BY Date DESC
        LIMIT p_Limit;
    END IF;

    -- Получаем имя активной модели пользователя через user_models
    SELECT g.Name INTO userName
    FROM users u
    LEFT JOIN user_models um ON u.Id = um.UserId AND um.IsActive = 1
    LEFT JOIN user_gpt g ON um.ModelId = g.Id
    WHERE u.Id = @user;

    -- Получаем имя респондера
    SELECT name INTO responderName
    FROM responders
    WHERE Id = @responder;

    -- Создаем JSON-объект
    SET dialogData = JSON_OBJECT(
        'Data', @data,
        'Type', @type,
        'Model', responderName,
        'Responder', userName,
        'Date', @date
    );

    RETURN dialogData;
END//
DELIMITER ;

-- Дамп структуры для функция air.ReadResponderName
DELIMITER //
CREATE FUNCTION `ReadResponderName`(`p_RespId` BIGINT
) RETURNS longtext CHARSET utf8mb4 COLLATE utf8mb4_bin
    COMMENT 'Возвращает Name и RealResponderId для респондера RespId'
BEGIN
    DECLARE userName TEXT;
    DECLARE responderId BIGINT;

    -- Извлечение данных из таблицы responders
    SELECT `Name`, `Id` 
    INTO userName, responderId
    FROM `responders`
    WHERE `RealId` = p_RespId
    LIMIT 1;

    -- Возвращаем Name и Id в формате JSON
    RETURN JSON_OBJECT(
        'Name', userName,
        'RespId', CAST(responderId AS UNSIGNED)
    );
END//
DELIMITER ;

-- Дамп структуры для таблица air.realtime_models
CREATE TABLE IF NOT EXISTS `realtime_models` (
  `Id` int(11) NOT NULL AUTO_INCREMENT,
  `Provider` tinyint(4) NOT NULL DEFAULT 1,
  `IsDefault` tinyint(1) NOT NULL DEFAULT 0,
  `Name` tinytext NOT NULL,
  `Updated` timestamp NOT NULL DEFAULT current_timestamp() ON UPDATE current_timestamp(),
  PRIMARY KEY (`Id`) USING BTREE,
  KEY `FK_provider` (`Provider`) USING BTREE,
  CONSTRAINT `FK_provider` FOREIGN KEY (`Provider`) REFERENCES `model_providers` (`Id`) ON DELETE NO ACTION ON UPDATE NO ACTION
) ENGINE=InnoDB AUTO_INCREMENT=11 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для событие air.ResetFlagsEvent
DELIMITER //
CREATE EVENT `ResetFlagsEvent` ON SCHEDULE EVERY 30 SECOND STARTS '2025-05-03 11:55:47' ON COMPLETION PRESERVE ENABLE DO CALL _scheduler_ResetUserFlags()//
DELIMITER ;

-- Дамп структуры для таблица air.responders
CREATE TABLE IF NOT EXISTS `responders` (
  `Id` bigint(20) NOT NULL AUTO_INCREMENT,
  `Date` datetime DEFAULT current_timestamp(),
  `Type` tinyint(4) NOT NULL DEFAULT 0,
  `Name` varchar(120) DEFAULT NULL,
  `RealId` bigint(20) DEFAULT NULL,
  PRIMARY KEY (`Id`),
  KEY `fk_chat_type_for_responders` (`Type`),
  CONSTRAINT `fk_chat_type_for_responders` FOREIGN KEY (`Type`) REFERENCES `chat_type` (`Id`)
) ENGINE=InnoDB AUTO_INCREMENT=151 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для процедура air.SaveChannelData
DELIMITER //
CREATE PROCEDURE `SaveChannelData`(
	IN `p_UserId` INT,
	IN `p_Type` VARCHAR(12),
	IN `p_Data` JSON,
	IN `p_Enabled` TINYINT(1)
)
BEGIN
    DECLARE is_exists INT DEFAULT 0;

    -- Проверяем существование записи
    SELECT COUNT(*) INTO is_exists FROM channels WHERE UserId = p_UserId;

    -- Если записи нет, создаем пустую запись с нужным UserId
    IF is_exists = 0 THEN
        INSERT INTO channels (UserId)
        VALUES (p_UserId);
    END IF;

    -- Обновляем только тот канал, который был передан
    IF p_Type = 'tgbot' THEN
        UPDATE channels
        SET TgBot = p_Data,
            TgBot_enabled = p_Enabled
        WHERE UserId = p_UserId;
    ELSEIF p_Type = 'widget' THEN
        UPDATE channels
        SET Widget = p_Data,
            Widget_enabled = p_Enabled
        WHERE UserId = p_UserId;
    ELSEIF p_Type = 'tgubot' THEN
        UPDATE channels
        SET TgUserBot = p_Data,
            TgUserBot_enabled = p_Enabled
        WHERE UserId = p_UserId;
	ELSEIF p_Type = 'whatsbot' THEN
        UPDATE channels
        SET Whats = p_Data,
            Whats_enabled = p_Enabled
        WHERE UserId = p_UserId; 
	ELSEIF p_Type = 'insta' THEN
        UPDATE channels
        SET Insta = p_Data,
            Insta_enabled = p_Enabled
        WHERE UserId = p_UserId;
   -- Для Avito данные сохраняются в процессе авторизации
	 END IF;
END//
DELIMITER ;

-- Дамп структуры для процедура air.SaveContext
DELIMITER //
CREATE PROCEDURE `SaveContext`(
	IN `p_DialogId` BIGINT,
	IN `p_Provider` ENUM('openai', 'mistral'),
	IN `p_Data` JSON
)
BEGIN
    DECLARE v_CurrentContext JSON;
    DECLARE v_NewContext JSON;
    
    -- Получаем текущий контекст
    SELECT `Context` INTO v_CurrentContext 
    FROM dialogs 
    WHERE Id = p_DialogId;
    
    -- Если контекста нет, создаём новый объект
    IF v_CurrentContext IS NULL THEN
        SET v_NewContext = JSON_OBJECT(p_Provider, p_Data);
    ELSE
        -- Обновляем или добавляем данные для провайдера, сохраняя данные других
        SET v_NewContext = JSON_SET(
            v_CurrentContext,
            CONCAT('$.', p_Provider),
            p_Data
        );
    END IF;
    
    -- Обновляем запись
    UPDATE dialogs
    SET `Context` = v_NewContext,
        `Date` = CURRENT_TIMESTAMP()
    WHERE Id = p_DialogId;
END//
DELIMITER ;

-- Дамп структуры для процедура air.SaveDialog
DELIMITER //
CREATE PROCEDURE `SaveDialog`(
	IN `p_DialogId` BIGINT,
	IN `p_Data` JSON
)
BEGIN
    -- Обновляем поле Data в существующей записи
    UPDATE dialogs
    SET Data = JSON_ARRAY_APPEND(IFNULL(Data, '[]'), '$', p_Data), Date = current_timestamp()
    WHERE Id = p_DialogId;
END//
DELIMITER ;

-- Дамп структуры для таблица air.service
CREATE TABLE IF NOT EXISTS `service` (
  `Id` int(11) NOT NULL AUTO_INCREMENT,
  `UserId` int(11) NOT NULL,
  `ServiceType` enum('lead-haunter') NOT NULL,
  `Updated` timestamp NOT NULL DEFAULT current_timestamp(),
  `Status` enum('Created','Completed','InProgress','Error','MsgLimit','Stopped') NOT NULL DEFAULT 'Created',
  `Config` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin DEFAULT NULL CHECK (json_valid(`Config`)),
  PRIMARY KEY (`Id`),
  UNIQUE KEY `uq_user_service` (`UserId`,`ServiceType`),
  CONSTRAINT `fk_service_user` FOREIGN KEY (`UserId`) REFERENCES `users` (`Id`) ON DELETE CASCADE ON UPDATE CASCADE
) ENGINE=InnoDB AUTO_INCREMENT=222 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.service_contacts
CREATE TABLE IF NOT EXISTS `service_contacts` (
  `Id` bigint(20) NOT NULL AUTO_INCREMENT,
  `UserId` int(11) NOT NULL,
  `Contact` varchar(32) DEFAULT NULL,
  `Result` enum('unprocessed','processed','absent','unanswered','answered','success','banned','blocked','other','retry_telegram','retry_whatsapp','retry_failed') NOT NULL DEFAULT 'unprocessed',
  `Added` timestamp NOT NULL DEFAULT current_timestamp(),
  `Updated` timestamp NOT NULL DEFAULT current_timestamp() ON UPDATE current_timestamp(),
  PRIMARY KEY (`Id`),
  UNIQUE KEY `uq_user_contact` (`UserId`,`Contact`),
  CONSTRAINT `FK1_uid` FOREIGN KEY (`UserId`) REFERENCES `users` (`Id`)
) ENGINE=InnoDB AUTO_INCREMENT=170 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.service_contact_availability
CREATE TABLE IF NOT EXISTS `service_contact_availability` (
  `Id` bigint(20) NOT NULL AUTO_INCREMENT,
  `ContactId` bigint(20) NOT NULL,
  `Provider` enum('telegram','whatsapp') NOT NULL,
  `IsAvailable` tinyint(1) NOT NULL DEFAULT 0,
  `CheckedAt` timestamp NOT NULL DEFAULT current_timestamp(),
  `UpdatedAt` timestamp NOT NULL DEFAULT current_timestamp() ON UPDATE current_timestamp(),
  PRIMARY KEY (`Id`),
  UNIQUE KEY `uq_contact_provider` (`ContactId`,`Provider`),
  KEY `idx_contact_id` (`ContactId`),
  CONSTRAINT `fk_availability_contact` FOREIGN KEY (`ContactId`) REFERENCES `service_contacts` (`Id`) ON DELETE CASCADE ON UPDATE CASCADE
) ENGINE=InnoDB AUTO_INCREMENT=25 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.service_dialogs
CREATE TABLE IF NOT EXISTS `service_dialogs` (
  `Id` bigint(20) NOT NULL AUTO_INCREMENT,
  `Responder` bigint(20) NOT NULL,
  `DialogId` bigint(20) DEFAULT NULL COMMENT 'Ссылка на основную таблицу dialogs (NULL для старых записей)',
  `CreatedAt` datetime NOT NULL DEFAULT current_timestamp(),
  PRIMARY KEY (`Id`),
  UNIQUE KEY `uq_service_dialog_responder` (`Responder`),
  KEY `FK2_resp` (`Responder`),
  KEY `idx_dialog_id` (`DialogId`),
  CONSTRAINT `FK2_resp` FOREIGN KEY (`Responder`) REFERENCES `service_contacts` (`Id`) ON DELETE CASCADE ON UPDATE CASCADE,
  CONSTRAINT `fk_service_dialogs_dialog` FOREIGN KEY (`DialogId`) REFERENCES `dialogs` (`Id`) ON DELETE CASCADE ON UPDATE CASCADE
) ENGINE=InnoDB AUTO_INCREMENT=30 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.service_gpt
CREATE TABLE IF NOT EXISTS `service_gpt` (
  `Id` bigint(20) NOT NULL AUTO_INCREMENT,
  `UserId` int(11) NOT NULL COMMENT 'ID пользователя',
  `ModelId` int(11) DEFAULT NULL COMMENT 'Ссылка на user_models.Id (активная модель для Telegram lead generation)',
  `Start` varchar(4096) NOT NULL COMMENT 'Стартовое сообщение для начала диалога',
  `TgGroup` tinytext NOT NULL COMMENT 'Telegram группа для пересылки успешных контактов',
  `AccessTime` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin DEFAULT NULL COMMENT 'Расписание доступности сервиса (JSON)',
  `CreatedAt` timestamp NOT NULL DEFAULT current_timestamp(),
  `UpdatedAt` timestamp NOT NULL DEFAULT current_timestamp() ON UPDATE current_timestamp(),
  PRIMARY KEY (`Id`),
  UNIQUE KEY `UserId` (`UserId`),
  KEY `FK_service_gpt_model` (`ModelId`),
  CONSTRAINT `FK_service_gpt_model` FOREIGN KEY (`ModelId`) REFERENCES `user_models` (`Id`) ON DELETE SET NULL,
  CONSTRAINT `FK_service_gpt_userid` FOREIGN KEY (`UserId`) REFERENCES `users` (`Id`) ON DELETE CASCADE ON UPDATE CASCADE
) ENGINE=InnoDB AUTO_INCREMENT=26 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='Конфигурация Telegram lead generation сервиса. Модель берется из user_models, здесь только параметры диалога.';

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.service_proxy
CREATE TABLE IF NOT EXISTS `service_proxy` (
  `Id` int(11) NOT NULL AUTO_INCREMENT,
  `UserId` int(11) NOT NULL,
  `ProxyId` int(11) NOT NULL,
  `Addr` varchar(255) NOT NULL,
  `Username` varchar(64) DEFAULT NULL,
  `Password` varchar(64) DEFAULT NULL,
  `ProxyType` enum('SOCKS5','HTTP','HTTPS','error') DEFAULT NULL,
  `IsActive` tinyint(1) NOT NULL DEFAULT 1,
  `CreatedAt` timestamp NOT NULL DEFAULT current_timestamp(),
  `UpdatedAt` timestamp NOT NULL DEFAULT current_timestamp() ON UPDATE current_timestamp(),
  PRIMARY KEY (`Id`) USING BTREE,
  UNIQUE KEY `uq_user_proxyid` (`UserId`,`ProxyId`) USING BTREE,
  UNIQUE KEY `uq_user_addr` (`UserId`,`Addr`) USING BTREE,
  KEY `idx_user_active` (`UserId`,`IsActive`) USING BTREE,
  CONSTRAINT `fk_service_proxy_user` FOREIGN KEY (`UserId`) REFERENCES `users` (`Id`) ON DELETE CASCADE ON UPDATE CASCADE
) ENGINE=InnoDB AUTO_INCREMENT=304 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.service_tgbots
CREATE TABLE IF NOT EXISTS `service_tgbots` (
  `Id` int(11) NOT NULL AUTO_INCREMENT,
  `UserId` int(11) NOT NULL,
  `BotId` int(11) NOT NULL,
  `BotAlias` varchar(100) NOT NULL,
  `AuthData` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `IsActive` tinyint(1) NOT NULL DEFAULT 1,
  `CreatedAt` timestamp NOT NULL DEFAULT current_timestamp(),
  `UpdatedAt` timestamp NOT NULL DEFAULT current_timestamp() ON UPDATE current_timestamp(),
  PRIMARY KEY (`Id`) USING BTREE,
  UNIQUE KEY `uq_user_alias` (`UserId`,`BotAlias`) USING BTREE,
  UNIQUE KEY `uq_user_botid` (`UserId`,`BotId`) USING BTREE,
  KEY `idx_user_active` (`UserId`,`IsActive`) USING BTREE,
  CONSTRAINT `fk_service_tgbots_user` FOREIGN KEY (`UserId`) REFERENCES `users` (`Id`) ON DELETE CASCADE ON UPDATE CASCADE
) ENGINE=InnoDB AUTO_INCREMENT=22 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.service_user_bot_events
CREATE TABLE IF NOT EXISTS `service_user_bot_events` (
  `BotType` enum('telegram','whatsapp') NOT NULL DEFAULT 'telegram',
  `Id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `BotId` int(11) NOT NULL,
  `EventType` enum('state','error','warning','info') NOT NULL,
  `CauseGroup` enum('successes','auth_key','session','phone','password','api','proxy','user_block','chat_access','content','media','rate_limit','retry','other') DEFAULT NULL,
  `CauseCode` varchar(64) DEFAULT NULL,
  `Description` varchar(255) DEFAULT NULL,
  `Context` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin DEFAULT NULL CHECK (json_valid(`Context`)),
  `Severity` enum('fatal','start_block','send_block','recoverable') NOT NULL DEFAULT 'recoverable',
  `WaitUntil` timestamp NULL DEFAULT NULL,
  `NewIsActive` tinyint(1) DEFAULT NULL,
  `CreatedAt` timestamp NOT NULL DEFAULT current_timestamp(),
  PRIMARY KEY (`Id`),
  KEY `idx_bot_type_id` (`BotType`,`BotId`),
  KEY `idx_bot_time` (`BotType`,`BotId`,`CreatedAt`),
  KEY `idx_bot_wait` (`BotType`,`BotId`,`WaitUntil`)
) ENGINE=InnoDB AUTO_INCREMENT=1323 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.service_user_bot_state
CREATE TABLE IF NOT EXISTS `service_user_bot_state` (
  `BotType` enum('telegram','whatsapp') NOT NULL DEFAULT 'telegram',
  `BotId` int(11) NOT NULL,
  `CanStart` tinyint(1) NOT NULL DEFAULT 1,
  `CanSend` tinyint(1) NOT NULL DEFAULT 1,
  `LastErrorGroup` enum('auth_key','session','phone','password','api','user_block','chat_access','content','media','rate_limit','other') DEFAULT NULL,
  `LastErrorCode` varchar(64) DEFAULT NULL,
  `LastErrorAt` timestamp NULL DEFAULT NULL,
  `RateLimitUntil` timestamp NULL DEFAULT NULL,
  `UpdatedAt` timestamp NOT NULL DEFAULT current_timestamp() ON UPDATE current_timestamp(),
  PRIMARY KEY (`BotType`,`BotId`),
  KEY `idx_state_flags` (`CanStart`,`CanSend`),
  KEY `idx_state_rate` (`RateLimitUntil`),
  KEY `idx_bot_type_id` (`BotType`,`BotId`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.service_wabots
CREATE TABLE IF NOT EXISTS `service_wabots` (
  `Id` int(11) NOT NULL AUTO_INCREMENT,
  `UserId` int(11) NOT NULL,
  `BotId` int(11) NOT NULL,
  `BotAlias` varchar(100) NOT NULL,
  `AuthData` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `IsActive` tinyint(1) NOT NULL DEFAULT 1,
  `CreatedAt` timestamp NOT NULL DEFAULT current_timestamp(),
  `UpdatedAt` timestamp NOT NULL DEFAULT current_timestamp() ON UPDATE current_timestamp(),
  PRIMARY KEY (`Id`) USING BTREE,
  UNIQUE KEY `uq_user_alias` (`UserId`,`BotAlias`) USING BTREE,
  UNIQUE KEY `uq_user_botid` (`UserId`,`BotId`) USING BTREE,
  KEY `idx_user_active` (`UserId`,`IsActive`) USING BTREE,
  CONSTRAINT `fk_service_wabots_user` FOREIGN KEY (`UserId`) REFERENCES `users` (`Id`) ON DELETE CASCADE ON UPDATE CASCADE
) ENGINE=InnoDB AUTO_INCREMENT=2 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для процедура air.SetChannelEnabled
DELIMITER //
CREATE PROCEDURE `SetChannelEnabled`(
	IN `input_UserId` INT,
	IN `input_ChannelType` VARCHAR(20),
	IN `input_Enabled` TINYINT(1)
)
BEGIN
    IF input_ChannelType = 'TgBot' THEN
        UPDATE channels
        SET TgBot_enabled = input_Enabled
        WHERE UserId = input_UserId;
    ELSEIF input_ChannelType = 'Widget' THEN
        UPDATE channels
        SET Widget_enabled = input_Enabled
        WHERE UserId = input_UserId;
    ELSEIF input_ChannelType = 'TgUserBot' THEN
        UPDATE channels
        SET TgUserBot_enabled = input_Enabled
        WHERE UserId = input_UserId;
    ELSEIF input_ChannelType = 'Whats' THEN
        UPDATE channels
        SET Whats_enabled = input_Enabled
        WHERE UserId = input_UserId;
	 ELSEIF input_ChannelType = 'Insta' THEN
        UPDATE channels
        SET Insta_enabled = input_Enabled
        WHERE UserId = input_UserId;
	 ELSEIF input_ChannelType = 'Avito' THEN
        UPDATE channels
        SET Avito_enabled = input_Enabled
        WHERE UserId = input_UserId;	      
    ELSE
        SIGNAL SQLSTATE '45000'
        SET MESSAGE_TEXT = 'Invalid ChannelType provided.';
    END IF;
END//
DELIMITER ;

-- Дамп структуры для таблица air.storage_migrations
CREATE TABLE IF NOT EXISTS `storage_migrations` (
  `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `user_id` int(11) NOT NULL,
  `source_type` enum('internal_minio','external_s3') NOT NULL,
  `target_type` enum('internal_minio','external_s3') NOT NULL,
  `state` enum('pending','running','failed','completed','cancelled') NOT NULL DEFAULT 'pending',
  `manifest` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin DEFAULT NULL CHECK (json_valid(`manifest`)),
  `last_error` text DEFAULT NULL,
  `created_at` timestamp NOT NULL DEFAULT current_timestamp(),
  `updated_at` timestamp NOT NULL DEFAULT current_timestamp() ON UPDATE current_timestamp(),
  PRIMARY KEY (`id`),
  KEY `idx_storage_migration_user_state` (`user_id`,`state`),
  CONSTRAINT `fk_storage_migration_user` FOREIGN KEY (`user_id`) REFERENCES `users` (`Id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.subscriptions
CREATE TABLE IF NOT EXISTS `subscriptions` (
  `Id` bigint(20) NOT NULL AUTO_INCREMENT,
  `UserId` int(11) NOT NULL,
  `StartDate` date NOT NULL,
  `MonthsPaid` int(11) NOT NULL DEFAULT 0,
  `TotalCost` decimal(10,2) NOT NULL DEFAULT 0.00,
  `Discount` decimal(10,2) DEFAULT 0.00,
  `EndDate` date DEFAULT NULL,
  `Notified` tinyint(1) NOT NULL DEFAULT 0,
  PRIMARY KEY (`Id`),
  KEY `UserId` (`UserId`),
  CONSTRAINT `subscriptions_ibfk_1` FOREIGN KEY (`UserId`) REFERENCES `users` (`Id`) ON DELETE CASCADE
) ENGINE=InnoDB AUTO_INCREMENT=24 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для процедура air.UpdateDialogsMeta
DELIMITER //
CREATE PROCEDURE `UpdateDialogsMeta`(
	IN `dialog_id` BIGINT,
	IN `meta` VARCHAR(10)
)
BEGIN
    IF meta = 'target' THEN
        UPDATE dialogs
        SET Target = 1
        WHERE Id = dialog_id;
    ELSEIF meta = 'trigger' THEN
        UPDATE dialogs
        SET `Trigger` = 1
        WHERE Id = dialog_id;
    ELSE
        SIGNAL SQLSTATE '45000'
        SET MESSAGE_TEXT = 'Invalid meta. Only "Target" or "Trigger" are allowed.';
    END IF;
END//
DELIMITER ;

-- Дамп структуры для функция air.UserLang
DELIMITER //
CREATE FUNCTION `UserLang`(`userId` INT
) RETURNS varchar(2) CHARSET utf8mb4 COLLATE utf8mb4_general_ci
BEGIN
	DECLARE userLang VARCHAR(2);
	
	SELECT languages.`Code` 
	INTO userLang
	FROM users
	JOIN languages ON users.Lang = languages.Id
	WHERE users.Id = userId;
	
	RETURN userLang;
END//
DELIMITER ;

-- Дамп структуры для таблица air.users
CREATE TABLE IF NOT EXISTS `users` (
  `Id` int(11) NOT NULL AUTO_INCREMENT,
  `Date` timestamp NULL DEFAULT current_timestamp(),
  `Name` tinytext NOT NULL COMMENT 'Отображаемое имя',
  `Lang` tinyint(4) DEFAULT NULL,
  `RoleId` tinyint(4) NOT NULL DEFAULT 1,
  `balance` decimal(10,2) NOT NULL DEFAULT 0.00,
  `currency` tinyint(4) DEFAULT NULL,
  `TimeZone` varchar(64) NOT NULL DEFAULT 'Europe/Amsterdam',
  `Timechange` timestamp NOT NULL DEFAULT current_timestamp(),
  PRIMARY KEY (`Id`) USING HASH,
  KEY `fk_users_roles` (`RoleId`) USING HASH,
  KEY `fk_currency_name` (`currency`),
  KEY `fk_lang` (`Lang`),
  KEY `fk_role` (`RoleId`),
  CONSTRAINT `fk_currency_name` FOREIGN KEY (`currency`) REFERENCES `currency` (`Id`),
  CONSTRAINT `fk_lang` FOREIGN KEY (`Lang`) REFERENCES `languages` (`Id`),
  CONSTRAINT `fk_role` FOREIGN KEY (`RoleId`) REFERENCES `user_roles` (`Id`)
) ENGINE=InnoDB AUTO_INCREMENT=100005 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.user_api_keys
CREATE TABLE IF NOT EXISTS `user_api_keys` (
  `Id` int(11) unsigned NOT NULL AUTO_INCREMENT,
  `UserId` int(11) NOT NULL,
  `Provider` enum('openai','mistral','google') CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci NOT NULL,
  `ApiKey` varchar(512) NOT NULL DEFAULT '',
  `CreatedAt` datetime NOT NULL DEFAULT current_timestamp(),
  `UpdatedAt` datetime NOT NULL DEFAULT current_timestamp() ON UPDATE current_timestamp(),
  PRIMARY KEY (`Id`),
  UNIQUE KEY `uq_user_provider` (`UserId`,`Provider`),
  CONSTRAINT `fk_uak_user` FOREIGN KEY (`UserId`) REFERENCES `users` (`Id`) ON DELETE CASCADE ON UPDATE CASCADE
) ENGINE=InnoDB AUTO_INCREMENT=15 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_uca1400_ai_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.user_auth
CREATE TABLE IF NOT EXISTS `user_auth` (
  `Id` int(11) NOT NULL AUTO_INCREMENT,
  `UserId` int(11) NOT NULL,
  `SHA` varchar(128) NOT NULL,
  `Email` varchar(512) NOT NULL,
  `EmailHash` varchar(64) DEFAULT NULL,
  `MasterKey` varchar(512) DEFAULT NULL,
  `WrapSalt` varchar(44) DEFAULT NULL,
  `TOTPSecret` varchar(128) DEFAULT NULL COMMENT 'Зашифрованный TOTP secret (base32)',
  `Confirmed` tinyint(1) NOT NULL DEFAULT 0,
  `Disabled` tinyint(1) NOT NULL DEFAULT 0,
  PRIMARY KEY (`Id`),
  UNIQUE KEY `Email` (`Email`),
  UNIQUE KEY `EmailHash_UNIQUE` (`EmailHash`) USING BTREE,
  KEY `user_auth_ibfk_1` (`UserId`),
  CONSTRAINT `user_auth_ibfk_1` FOREIGN KEY (`UserId`) REFERENCES `users` (`Id`) ON DELETE CASCADE ON UPDATE CASCADE
) ENGINE=InnoDB AUTO_INCREMENT=46 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.user_gpt
CREATE TABLE IF NOT EXISTS `user_gpt` (
  `Id` int(11) NOT NULL AUTO_INCREMENT,
  `Name` tinytext NOT NULL,
  `Model` int(11) NOT NULL DEFAULT 2,
  `Realtime` int(11) DEFAULT NULL,
  `Provider` tinyint(4) NOT NULL DEFAULT 1,
  `AssistantId` tinytext NOT NULL,
  `Data` blob DEFAULT NULL COMMENT 'Сжатый контекст',
  `Ids` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin DEFAULT NULL,
  PRIMARY KEY (`Id`),
  KEY `idx_user_gpt_model_v2` (`Model`),
  KEY `idx_user_gpt_provider_v2` (`Provider`),
  KEY `idx_user_gpt_realtime_v2` (`Realtime`),
  CONSTRAINT `FK_user_gpt_gpt_models` FOREIGN KEY (`Model`) REFERENCES `gpt_models` (`Id`),
  CONSTRAINT `fk_user_gpt_model_v2` FOREIGN KEY (`Model`) REFERENCES `gpt_models` (`Id`),
  CONSTRAINT `fk_user_gpt_provider` FOREIGN KEY (`Provider`) REFERENCES `model_providers` (`Id`),
  CONSTRAINT `fk_user_gpt_provider_v2` FOREIGN KEY (`Provider`) REFERENCES `model_providers` (`Id`),
  CONSTRAINT `fk_user_gpt_realtime_v2` FOREIGN KEY (`Realtime`) REFERENCES `realtime_models` (`Id`),
  CONSTRAINT `chk_user_gpt_ids` CHECK (json_valid(`Ids`))
) ENGINE=InnoDB AUTO_INCREMENT=112 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.user_models
CREATE TABLE IF NOT EXISTS `user_models` (
  `Id` int(11) NOT NULL AUTO_INCREMENT,
  `UserId` int(11) NOT NULL COMMENT 'ID пользователя',
  `ModelId` int(11) NOT NULL COMMENT 'ID модели из user_gpt',
  `Provider` tinyint(4) NOT NULL COMMENT '1=OpenAI, 2=Mistral, 3=Google',
  `IsActive` tinyint(1) NOT NULL COMMENT 'Активная модель для пользователя',
  `CreatedAt` timestamp NOT NULL DEFAULT current_timestamp(),
  PRIMARY KEY (`Id`) USING BTREE,
  UNIQUE KEY `idx_user_model` (`UserId`,`ModelId`) USING BTREE,
  KEY `idx_user_provider` (`UserId`,`Provider`) USING BTREE,
  KEY `fk_user_models_model` (`ModelId`),
  KEY `fk_user_models_provider` (`Provider`),
  CONSTRAINT `fk_user_models_model` FOREIGN KEY (`ModelId`) REFERENCES `user_gpt` (`Id`) ON DELETE CASCADE,
  CONSTRAINT `fk_user_models_provider` FOREIGN KEY (`Provider`) REFERENCES `model_providers` (`Id`),
  CONSTRAINT `fk_user_models_user` FOREIGN KEY (`UserId`) REFERENCES `users` (`Id`) ON DELETE CASCADE
) ENGINE=InnoDB AUTO_INCREMENT=36 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.user_roles
CREATE TABLE IF NOT EXISTS `user_roles` (
  `Id` tinyint(4) NOT NULL DEFAULT 0,
  `RoleName` varchar(50) NOT NULL,
  PRIMARY KEY (`Id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.user_storage_config
CREATE TABLE IF NOT EXISTS `user_storage_config` (
  `user_id` int(11) NOT NULL,
  `storage_type` enum('internal_minio','external_s3') NOT NULL DEFAULT 'internal_minio',
  `endpoint` varchar(512) DEFAULT NULL,
  `bucket` varchar(255) DEFAULT NULL,
  `region` varchar(64) DEFAULT NULL,
  `access_key_ciphertext` text DEFAULT NULL,
  `secret_key_ciphertext` text DEFAULT NULL,
  `external_sts_supported` tinyint(1) NOT NULL DEFAULT 0,
  `created_at` timestamp NOT NULL DEFAULT current_timestamp(),
  `updated_at` timestamp NOT NULL DEFAULT current_timestamp() ON UPDATE current_timestamp(),
  PRIMARY KEY (`user_id`),
  CONSTRAINT `fk_storage_config_user` FOREIGN KEY (`user_id`) REFERENCES `users` (`Id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.user_storage_quota
CREATE TABLE IF NOT EXISTS `user_storage_quota` (
  `user_id` int(11) NOT NULL,
  `quota_bytes` bigint(20) unsigned NOT NULL DEFAULT 0,
  `used_bytes` bigint(20) unsigned NOT NULL DEFAULT 0,
  `reserved_bytes` bigint(20) unsigned NOT NULL DEFAULT 0,
  `created_at` timestamp NOT NULL DEFAULT current_timestamp(),
  `updated_at` timestamp NOT NULL DEFAULT current_timestamp() ON UPDATE current_timestamp(),
  PRIMARY KEY (`user_id`),
  CONSTRAINT `fk_storage_quota_user` FOREIGN KEY (`user_id`) REFERENCES `users` (`Id`) ON DELETE CASCADE,
  CONSTRAINT `chk_storage_quota_usage` CHECK (`used_bytes` + `reserved_bytes` >= `used_bytes`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Экспортируемые данные не выделены.

-- Дамп структуры для таблица air.vector_embeddings
CREATE TABLE IF NOT EXISTS `vector_embeddings` (
  `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT COMMENT 'Уникальный ID записи',
  `user_id` int(10) unsigned NOT NULL COMMENT 'ID пользователя',
  `model_id` int(11) NOT NULL COMMENT 'ID модели из user_models.ModelId',
  `provider` enum('openai','google') DEFAULT NULL COMMENT 'Провайдер эмбеддинга (google, openai)',
  `doc_id` varchar(255) NOT NULL COMMENT 'Уникальный ID документа',
  `doc_name` varchar(500) NOT NULL COMMENT 'Название документа',
  `content` text NOT NULL COMMENT 'Текстовое содержимое документа',
  `embedding` vector(3072) NOT NULL COMMENT 'Векторное представление (до 3072 float32, padded нулями)',
  `embedding_dim` smallint(5) unsigned NOT NULL DEFAULT 768 COMMENT 'Реальная размерность вектора (512/768/1536/3072)',
  `metadata` longtext CHARACTER SET utf8mb4 COLLATE utf8mb4_bin DEFAULT NULL COMMENT 'Дополнительные метаданные (категория, язык, теги и т.д.)',
  `created_at` timestamp NULL DEFAULT current_timestamp() COMMENT 'Дата создания',
  `updated_at` timestamp NULL DEFAULT current_timestamp() ON UPDATE current_timestamp() COMMENT 'Дата последнего обновления',
  PRIMARY KEY (`id`) USING BTREE,
  UNIQUE KEY `unique_model_doc` (`model_id`,`doc_id`) USING BTREE,
  KEY `idx_user_id` (`user_id`) USING BTREE COMMENT 'Быстрый поиск по пользователю',
  KEY `idx_created_at` (`created_at`) USING BTREE COMMENT 'Сортировка по дате создания',
  KEY `idx_doc_name` (`doc_name`(191)) USING BTREE COMMENT 'Поиск по названию документа',
  KEY `idx_model_id` (`model_id`) USING BTREE,
  KEY `idx_provider` (`provider`) USING BTREE COMMENT 'Фильтрация по провайдеру',
  KEY `idx_embedding_dim` (`embedding_dim`) USING BTREE COMMENT 'Фильтрация по размерности вектора',
  CONSTRAINT `fk_embeddings_model` FOREIGN KEY (`model_id`) REFERENCES `user_models` (`ModelId`) ON DELETE CASCADE,
  CONSTRAINT `metadata` CHECK (json_valid(`metadata`))
) ENGINE=InnoDB AUTO_INCREMENT=17 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Векторные эмбеддинги для семантического поиска (Google 768D, OpenAI 512-3072D + MariaDB VECTOR)';

-- Экспортируемые данные не выделены.

-- Дамп структуры для триггер air.tg_delete_tgbot_events
SET @OLDTMP_SQL_MODE=@@SQL_MODE, SQL_MODE='STRICT_TRANS_TABLES,ERROR_FOR_DIVISION_BY_ZERO,NO_AUTO_CREATE_USER,NO_ENGINE_SUBSTITUTION';
DELIMITER //
CREATE TRIGGER tg_delete_tgbot_events
AFTER DELETE ON service_tgbots
FOR EACH ROW
DELETE FROM service_user_bot_events
WHERE BotType = 'telegram' AND BotId = OLD.BotId//
DELIMITER ;
SET SQL_MODE=@OLDTMP_SQL_MODE;

-- Дамп структуры для триггер air.tg_delete_tgbot_state
SET @OLDTMP_SQL_MODE=@@SQL_MODE, SQL_MODE='STRICT_TRANS_TABLES,ERROR_FOR_DIVISION_BY_ZERO,NO_AUTO_CREATE_USER,NO_ENGINE_SUBSTITUTION';
DELIMITER //
CREATE TRIGGER tg_delete_tgbot_state
AFTER DELETE ON service_tgbots
FOR EACH ROW
DELETE FROM service_user_bot_state
WHERE BotType = 'telegram' AND BotId = OLD.BotId//
DELIMITER ;
SET SQL_MODE=@OLDTMP_SQL_MODE;

-- Дамп структуры для триггер air.tg_delete_wabot_events
SET @OLDTMP_SQL_MODE=@@SQL_MODE, SQL_MODE='STRICT_TRANS_TABLES,ERROR_FOR_DIVISION_BY_ZERO,NO_AUTO_CREATE_USER,NO_ENGINE_SUBSTITUTION';
DELIMITER //
CREATE TRIGGER tg_delete_wabot_events
AFTER DELETE ON service_wabots
FOR EACH ROW
DELETE FROM service_user_bot_events
WHERE BotType = 'whatsapp' AND BotId = OLD.BotId//
DELIMITER ;
SET SQL_MODE=@OLDTMP_SQL_MODE;

-- Дамп структуры для триггер air.tg_delete_wabot_state
SET @OLDTMP_SQL_MODE=@@SQL_MODE, SQL_MODE='STRICT_TRANS_TABLES,ERROR_FOR_DIVISION_BY_ZERO,NO_AUTO_CREATE_USER,NO_ENGINE_SUBSTITUTION';
DELIMITER //
CREATE TRIGGER tg_delete_wabot_state
AFTER DELETE ON service_wabots
FOR EACH ROW
DELETE FROM service_user_bot_state
WHERE BotType = 'whatsapp' AND BotId = OLD.BotId//
DELIMITER ;
SET SQL_MODE=@OLDTMP_SQL_MODE;

-- Дамп структуры для триггер air.trg_wabot_delete_cascade_events
SET @OLDTMP_SQL_MODE=@@SQL_MODE, SQL_MODE='STRICT_TRANS_TABLES,ERROR_FOR_DIVISION_BY_ZERO,NO_AUTO_CREATE_USER,NO_ENGINE_SUBSTITUTION';
DELIMITER //
CREATE TRIGGER `trg_wabot_delete_cascade_events`
AFTER DELETE ON `service_wabots`
FOR EACH ROW
BEGIN
    DELETE FROM service_user_bot_events
    WHERE BotType = 'whatsapp' AND BotId = OLD.Id;
END//
DELIMITER ;
SET SQL_MODE=@OLDTMP_SQL_MODE;

-- Дамп структуры для триггер air.trg_wabot_delete_cascade_state
SET @OLDTMP_SQL_MODE=@@SQL_MODE, SQL_MODE='STRICT_TRANS_TABLES,ERROR_FOR_DIVISION_BY_ZERO,NO_AUTO_CREATE_USER,NO_ENGINE_SUBSTITUTION';
DELIMITER //
CREATE TRIGGER `trg_wabot_delete_cascade_state`
AFTER DELETE ON `service_wabots`
FOR EACH ROW
BEGIN
    DELETE FROM service_user_bot_state
    WHERE BotType = 'whatsapp' AND BotId = OLD.Id;
END//
DELIMITER ;
SET SQL_MODE=@OLDTMP_SQL_MODE;

/*!40103 SET TIME_ZONE=IFNULL(@OLD_TIME_ZONE, 'system') */;
/*!40101 SET SQL_MODE=IFNULL(@OLD_SQL_MODE, '') */;
/*!40014 SET FOREIGN_KEY_CHECKS=IFNULL(@OLD_FOREIGN_KEY_CHECKS, 1) */;
/*!40101 SET CHARACTER_SET_CLIENT=@OLD_CHARACTER_SET_CLIENT */;
/*!40111 SET SQL_NOTES=IFNULL(@OLD_SQL_NOTES, 1) */;
