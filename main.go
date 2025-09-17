// main.go
package main

import (
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
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

	// Новый параметр: отвечать только на упоминания
	mentionOnly := strings.ToLower(getEnv("MENTION_ONLY", "false")) == "true"

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

	// Создание клиента
	client := twitch.NewClient(botUsername, oauthToken)

	// Обработчик сообщений
	client.OnPrivateMessage(func(message twitch.PrivateMessage) {
		// Проверяем, нужно ли отвечать только на упоминания
		if mentionOnly {
			// Режим "только упоминания" - отвечаем только если есть @botname
			if strings.Contains(message.Message, "@"+botUsername) {
				processCommand(client, message, commands, channel, botUsername)
			}
		} else {
			// Режим "все команды" - отвечаем на упоминания и прямые команды
			botMentioned := strings.Contains(message.Message, "@"+botUsername)
			directCommand := strings.HasPrefix(strings.TrimSpace(message.Message), "!")

			if botMentioned || directCommand {
				processCommand(client, message, commands, channel, botUsername)
			}
		}
	})

	slog.Info("Бот запущен",
		"channel", channel,
		"bot_username", botUsername,
		"mention_only", mentionOnly)

	// Подключение к каналу
	client.Join(channel)

	// Запуск клиента
	err = client.Connect()
	if err != nil {
		slog.Error("Ошибка подключения", "error", err)
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

func processCommand(client *twitch.Client, message twitch.PrivateMessage, commands map[string]string, channel string, botUsername string) {
	// Удаление упоминания бота из сообщения для извлечения команды
	cleanMessage := strings.TrimSpace(strings.Replace(message.Message, "@"+botUsername, "", 1))

	// Извлечение команды
	commandParts := strings.Fields(cleanMessage)
	if len(commandParts) == 0 {
		return
	}

	cmd := commandParts[0]

	// Поиск команды в конфигурации
	if response, exists := commands[cmd]; exists {
		time.Sleep(1 * time.Second)
		client.Say(channel, response)
		slog.Info("Команда выполнена", "user", message.User.Name, "command", cmd, "response", response)
	} else {
		slog.Debug("Неизвестная команда", "command", cmd, "user", message.User.Name)
		// Отправляем сообщение о неизвестной команде
		if strings.ToLower(getEnv("MENTION_ONLY", "false")) == "true" {
			time.Sleep(1 * time.Second)
			client.Say(channel, fmt.Sprintf("@%s Неизвестная команда. Используйте !commands для списка команд.", message.User.Name))
		}
	}
}
