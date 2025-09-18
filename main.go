// main.go
package main

import (
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gempir/go-twitch-irc/v4"
	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

type Command struct {
	Command string `yaml:"command"`
	Text    string `yaml:"text"`
}

type CommandsConfig struct {
	Messages []Command `yaml:"messages"`
}

// Структура для отслеживания глобального cooldown
type GlobalCooldownManager struct {
	mu       sync.Mutex
	lastUsed time.Time
	duration time.Duration
}

func NewGlobalCooldownManager(duration time.Duration) *GlobalCooldownManager {
	return &GlobalCooldownManager{
		duration: duration,
	}
}

func (gcm *GlobalCooldownManager) CanUse() bool {
	gcm.mu.Lock()
	defer gcm.mu.Unlock()

	return time.Since(gcm.lastUsed) >= gcm.duration
}

func (gcm *GlobalCooldownManager) Use() {
	gcm.mu.Lock()
	defer gcm.mu.Unlock()

	gcm.lastUsed = time.Now()
}

type Bot struct {
	client      *twitch.Client
	commands    map[string]string
	cooldown    *GlobalCooldownManager
	botUsername string
	channel     string
	mentionOnly bool
}

func main() {
	// Загрузка переменных окружения
	if err := godotenv.Load(); err != nil {
		fmt.Println("Предупреждение: Ошибка загрузки .env файла:", err)
	}

	// Настройка логгирования
	setupLogging()

	botUsername := getEnv("TWITCH_BOT_USERNAME", "")
	oauthToken := getEnv("TWITCH_OAUTH_TOKEN", "")
	channel := getEnv("TWITCH_CHANNEL", "")

	// Параметр: отвечать только на упоминания
	mentionOnly := strings.ToLower(getEnv("MENTION_ONLY", "false")) == "true"

	// Параметр cooldown в секундах (по умолчанию 15 секунд)
	cooldownSeconds := getEnvInt("COOLDOWN_SECONDS", 15)

	if botUsername == "" || oauthToken == "" || channel == "" {
		slog.Error("Не все обязательные переменные окружения заданы")
		return
	}

	// Загрузка команд из файла
	commands, err := loadCommands("commands.yaml")
	if err != nil {
		slog.Error("Ошибка загрузки команд", "error", err)
		return
	}

	// Добавляем команду для вывода всех зарегистрированных команд
	commands["!пасты"] = getAllCommandsText(commands)

	// Создание менеджера глобального cooldown
	cooldownManager := NewGlobalCooldownManager(time.Duration(cooldownSeconds) * time.Second)

	// Создание бота
	bot := &Bot{
		commands:    commands,
		cooldown:    cooldownManager,
		botUsername: botUsername,
		channel:     channel,
		mentionOnly: mentionOnly,
	}

	// Создание клиента
	client := twitch.NewClient(botUsername, oauthToken)
	bot.client = client

	// Обработчик сообщений
	client.OnPrivateMessage(func(message twitch.PrivateMessage) {
		bot.handleMessage(message)
	})

	slog.Info("Бот запущен",
		"channel", channel,
		"bot_username", botUsername,
		"mention_only", mentionOnly,
		"cooldown_seconds", cooldownSeconds)

	// Подключение к каналу
	client.Join(channel)

	// Запуск клиента
	err = client.Connect()
	if err != nil {
		slog.Error("Ошибка подключения", "error", err)
	}
}

func (b *Bot) handleMessage(message twitch.PrivateMessage) {
	// Проверяем глобальный cooldown
	if !b.cooldown.CanUse() {
		slog.Debug("Бот в cooldown")
		return
	}

	// Проверяем, нужно ли отвечать только на упоминания
	if b.mentionOnly {
		// Режим "только упоминания" - отвечаем только если есть @botname
		if strings.Contains(message.Message, "@"+b.botUsername) {
			b.processCommand(message)
		}
	} else {
		// Режим "все команды" - отвечаем на упоминания и прямые команды
		botMentioned := strings.Contains(message.Message, "@"+b.botUsername)
		directCommand := strings.HasPrefix(strings.TrimSpace(message.Message), "!")

		if botMentioned || directCommand {
			b.processCommand(message)
		}
	}
}

func (b *Bot) processCommand(message twitch.PrivateMessage) {
	// Удаление упоминания бота из сообщения для извлечения команды
	cleanMessage := strings.TrimSpace(strings.Replace(message.Message, "@"+b.botUsername, "", 1))

	if message.User.Name == b.botUsername {
		time.Sleep(1 * time.Second)
	}

	// Извлечение команды
	commandParts := strings.Fields(cleanMessage)
	if len(commandParts) == 0 {
		return
	}

	cmd := commandParts[0]

	// Поиск команды в конфигурации
	if response, exists := b.commands[cmd]; exists {
		// Устанавливаем глобальный cooldown перед отправкой ответа
		b.cooldown.Use()

		b.client.Say(b.channel, response)
		slog.Info("Команда выполнена",
			"user", message.User.Name,
			"command", cmd,
			"response", response)
	} else {
		slog.Debug("Неизвестная команда", "command", cmd, "user", message.User.Name)
		// Отправляем сообщение о неизвестной команде (без cooldown для этого сообщения)
		if strings.ToLower(getEnv("MENTION_ONLY", "false")) == "true" {
			b.client.Say(b.channel, fmt.Sprintf("@%s Неизвестная команда. Используйте !пасты для списка команд.", message.User.Name))
		}
	}
}

func setupLogging() {
	logLevel := getEnv("LOG_LEVEL", "INFO")
	logFile := getEnv("LOG_FILE", "")

	var level slog.Level
	switch strings.ToUpper(logLevel) {
	case "DEBUG":
		level = slog.LevelDebug
	case "INFO":
		level = slog.LevelInfo
	case "WARN":
		level = slog.LevelWarn
	case "ERROR":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	var handler slog.Handler
	if logFile != "" {
		// Логирование в файл
		file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			fmt.Printf("Ошибка создания файла логов %s: %v\n", logFile, err)
			// Используем stdout если файл не создался
			handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})
		} else {
			handler = slog.NewTextHandler(file, &slog.HandlerOptions{Level: level})
		}
	} else {
		// Логирование в stdout
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	valueStr := getEnv(key, "")
	if valueStr == "" {
		return defaultValue
	}

	var result int
	_, err := fmt.Sscanf(valueStr, "%d", &result)
	if err != nil {
		return defaultValue
	}

	return result
}

func loadCommands(filename string) (map[string]string, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения файла %s: %w", filename, err)
	}

	var config CommandsConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("ошибка парсинга YAML: %w", err)
	}

	commands := make(map[string]string)
	for _, cmd := range config.Messages {
		commands[cmd.Command] = cmd.Text
	}

	slog.Info("Команды загружены", "count", len(commands))
	for cmd := range commands {
		slog.Debug("Загружена команда", "command", cmd)
	}

	return commands, nil
}

func getAllCommandsText(commands map[string]string) string {
	var commandList []string
	for cmd := range commands {
		commandList = append(commandList, cmd)
	}
	sort.Strings(commandList)
	return "Доступные команды: " + strings.Join(commandList, ", ")
}
