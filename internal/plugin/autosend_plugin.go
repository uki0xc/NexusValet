package plugin

import (
	"context"
	"database/sql"
	"fmt"
	"nexusvalet/internal/command"
	"nexusvalet/internal/peers"
	"nexusvalet/pkg/logger"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/tg"
	"github.com/robfig/cron/v3"
)

// AutoSendTask 代表一个自动发送任务
type AutoSendTask struct {
	ID       int64        `json:"id"`
	ChatID   int64        `json:"chat_id"`
	Message  string       `json:"message"`
	CronExpr string       `json:"cron_expr"` // cron表达式
	NextRun  time.Time    `json:"next_run"`  // 下次运行时间（仅用于显示）
	Enabled  bool         `json:"enabled"`
	Created  time.Time    `json:"created"`
	cronID   cron.EntryID // cron任务ID，用于管理任务
}

// AutoSendPlugin 自动发送插件
type AutoSendPlugin struct {
	*BasePlugin
	db                *sql.DB
	telegramAPI       *tg.Client
	peerResolver      *peers.Resolver
	accessHashManager *AccessHashManager
	tasks             map[int64]*AutoSendTask
	tasksMutex        sync.RWMutex
	cronScheduler     *cron.Cron
	running           bool
}

// NewAutoSendPlugin 创建自动发送插件
func NewAutoSendPlugin(db *sql.DB) *AutoSendPlugin {
	info := &PluginInfo{
		PluginVersion: &PluginVersion{
			Name:        "autosend",
			Version:     "1.0.0",
			Author:      "NexusValet",
			Description: "定时自动发送消息插件，支持创建、管理和执行定时发送任务",
		},
		Dir:     "builtin",
		Enabled: true,
	}

	plugin := &AutoSendPlugin{
		BasePlugin:    NewBasePlugin(info),
		db:            db,
		tasks:         make(map[int64]*AutoSendTask),
		cronScheduler: cron.New(cron.WithSeconds()), // 支持秒级精度
		running:       false,
	}

	return plugin
}

// Initialize 初始化插件
func (asp *AutoSendPlugin) Initialize(ctx context.Context, manager interface{}) error {
	if err := asp.BasePlugin.Initialize(ctx, manager); err != nil {
		return err
	}

	// 初始化数据库表
	if err := asp.initDatabase(); err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}

	// 加载现有任务
	if err := asp.loadTasks(); err != nil {
		return fmt.Errorf("failed to load tasks: %w", err)
	}

	// 启动定时器
	asp.startScheduler()

	logger.Infof("AutoSend plugin initialized successfully")
	return nil
}

// Shutdown 关闭插件
func (asp *AutoSendPlugin) Shutdown(ctx context.Context) error {
	asp.stopScheduler()
	return asp.BasePlugin.Shutdown(ctx)
}

// SetTelegramClient 设置Telegram客户端
func (asp *AutoSendPlugin) SetTelegramClient(client *tg.Client, peerResolver *peers.Resolver) {
	asp.telegramAPI = client
	asp.peerResolver = peerResolver
	asp.accessHashManager = NewAccessHashManager(client)
}

// RegisterCommands 注册命令
func (asp *AutoSendPlugin) RegisterCommands(parser *command.Parser) error {
	// 注册主命令
	parser.RegisterCommand("autosend", "定时自动发送消息管理", asp.info.Name, asp.handleAutoSend)
	parser.RegisterCommand("as", "autosend简写命令", asp.info.Name, asp.handleAutoSend)

	logger.Infof("AutoSend commands registered successfully")
	return nil
}

// initDatabase 初始化数据库表
func (asp *AutoSendPlugin) initDatabase() error {
	// 首先检查表是否存在
	var count int
	err := asp.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='autosend_tasks'").Scan(&count)
	if err != nil {
		return err
	}

	if count == 0 {
		// 创建新表
		createTableSQL := `
		CREATE TABLE autosend_tasks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			message TEXT NOT NULL,
			cron_expr TEXT NOT NULL,
			next_run DATETIME,
			enabled BOOLEAN NOT NULL DEFAULT 1,
			created DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		`
		_, err = asp.db.Exec(createTableSQL)
		return err
	} else {
		// 检查是否需要添加新列或迁移数据
		rows, err := asp.db.Query("PRAGMA table_info(autosend_tasks)")
		if err != nil {
			return err
		}
		defer rows.Close()

		hasCronExprColumn := false
		hasOldColumns := false

		for rows.Next() {
			var cid int
			var name, dataType string
			var notNull, pk bool
			var defaultValue interface{}

			err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &pk)
			if err != nil {
				continue
			}

			if name == "cron_expr" {
				hasCronExprColumn = true
			}
			if name == "type" || name == "interval_seconds" || name == "daily_at" {
				hasOldColumns = true
			}
		}

		// 如果没有cron_expr列，需要迁移
		if !hasCronExprColumn {
			// 添加新列
			_, err = asp.db.Exec("ALTER TABLE autosend_tasks ADD COLUMN cron_expr TEXT")
			if err != nil {
				return err
			}

			// 如果有旧列，进行数据迁移
			if hasOldColumns {
				err = asp.migrateOldTasks()
				if err != nil {
					logger.Warnf("Failed to migrate old tasks: %v", err)
				}

				// 迁移完成后，为旧字段设置默认值以避免NOT NULL约束问题
				_, err = asp.db.Exec("UPDATE autosend_tasks SET interval_seconds = 0 WHERE interval_seconds IS NULL")
				if err != nil {
					logger.Warnf("Failed to update interval_seconds default values: %v", err)
				}
			}
		}
	}

	return nil
}

// migrateOldTasks 迁移旧的任务数据到新的cron格式
func (asp *AutoSendPlugin) migrateOldTasks() error {
	// 查询所有旧任务
	rows, err := asp.db.Query(`
		SELECT id, COALESCE(type, 'interval') as type, 
		       COALESCE(interval_seconds, 0) as interval_seconds, 
		       COALESCE(daily_at, '') as daily_at
		FROM autosend_tasks WHERE cron_expr IS NULL OR cron_expr = ''
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		var taskType string
		var intervalSeconds int
		var dailyAt string

		err := rows.Scan(&id, &taskType, &intervalSeconds, &dailyAt)
		if err != nil {
			continue
		}

		var cronExpr string
		if taskType == "daily" && dailyAt != "" {
			// 转换每日任务为cron表达式
			parts := strings.Split(dailyAt, ":")
			if len(parts) == 2 {
				hour := parts[0]
				minute := parts[1]
				cronExpr = fmt.Sprintf("0 %s %s * * *", minute, hour) // 秒 分 时 日 月 周
			}
		} else if taskType == "interval" && intervalSeconds > 0 {
			// 间隔任务转换为每N秒执行的cron（如果可能）
			if intervalSeconds >= 60 && intervalSeconds%60 == 0 {
				minutes := intervalSeconds / 60
				if minutes <= 59 {
					cronExpr = fmt.Sprintf("0 */%d * * * *", minutes) // 每N分钟
				}
			}
			// 对于不能转换的间隔任务，删除它们
			if cronExpr == "" {
				_, err = asp.db.Exec("DELETE FROM autosend_tasks WHERE id = ?", id)
				if err != nil {
					logger.Errorf("Failed to delete unconvertible task %d: %v", id, err)
				}
				logger.Infof("Deleted unconvertible interval task %d (%d seconds)", id, intervalSeconds)
				continue
			}
		}

		if cronExpr != "" {
			// 更新任务的cron表达式
			_, err = asp.db.Exec("UPDATE autosend_tasks SET cron_expr = ? WHERE id = ?", cronExpr, id)
			if err != nil {
				logger.Errorf("Failed to update task %d with cron expression: %v", id, err)
			} else {
				logger.Infof("Migrated task %d to cron expression: %s", id, cronExpr)
			}
		}
	}

	return nil
}

// loadTasks 从数据库加载任务
func (asp *AutoSendPlugin) loadTasks() error {
	rows, err := asp.db.Query(`
		SELECT id, chat_id, message, cron_expr, enabled, created, COALESCE(next_run, '') as next_run
		FROM autosend_tasks WHERE enabled = 1 AND cron_expr IS NOT NULL AND cron_expr != ''
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	asp.tasksMutex.Lock()
	defer asp.tasksMutex.Unlock()

	for rows.Next() {
		var task AutoSendTask
		var createdStr, nextRunStr string

		err := rows.Scan(&task.ID, &task.ChatID, &task.Message, &task.CronExpr, &task.Enabled, &createdStr, &nextRunStr)
		if err != nil {
			logger.Errorf("Failed to scan task: %v", err)
			continue
		}

		// 解析创建时间 - 支持多种时间格式
		task.Created, err = asp.parseFlexibleTimeString(createdStr)
		if err != nil {
			logger.Errorf("Failed to parse created time: %v", err)
			continue
		}

		// 解析下次运行时间，如果解析失败则计算新的
		if nextRunStr != "" {
			if task.NextRun, err = asp.parseFlexibleTimeString(nextRunStr); err != nil {
				logger.Warnf("Failed to parse next_run time for task %d: %v", task.ID, err)
			}
		}

		// 如果NextRun为空或已过期，重新计算
		if task.NextRun.IsZero() || task.NextRun.Before(time.Now()) {
			parser := cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
			if schedule, err := parser.Parse(task.CronExpr); err == nil {
				task.NextRun = schedule.Next(time.Now())
			}
		}

		// 添加到cron调度器
		cronID, err := asp.cronScheduler.AddFunc(task.CronExpr, func() {
			asp.executeTask(&task)
		})
		if err != nil {
			logger.Errorf("Failed to add cron task %d: %v", task.ID, err)
			continue
		}

		task.cronID = cronID
		asp.tasks[task.ID] = &task
	}

	logger.Infof("Loaded %d autosend tasks", len(asp.tasks))
	return nil
}

// startScheduler 启动调度器
func (asp *AutoSendPlugin) startScheduler() {
	if asp.running {
		return
	}

	asp.running = true
	asp.cronScheduler.Start()
	logger.Infof("AutoSend cron scheduler started")
}

// stopScheduler 停止调度器
func (asp *AutoSendPlugin) stopScheduler() {
	if !asp.running {
		return
	}

	asp.running = false
	if asp.cronScheduler != nil {
		asp.cronScheduler.Stop()
		logger.Infof("AutoSend cron scheduler stopped")
	}
}

// executeTask 执行单个任务
func (asp *AutoSendPlugin) executeTask(task *AutoSendTask) {
	if asp.telegramAPI == nil || asp.peerResolver == nil {
		logger.Errorf("Telegram API or peer resolver not available")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 尝试发送消息，带重试机制
	success := asp.sendMessageWithRetry(ctx, task)
	if success {
		logger.Infof("AutoSend task %d executed successfully (cron: %s)", task.ID, task.CronExpr)
	} else {
		logger.Errorf("AutoSend task %d failed after all retry attempts", task.ID)
		// 可选：禁用失败的任务以避免持续错误
		asp.handleFailedTask(task)
	}
}

// sendMessageWithRetry 带重试机制的消息发送
func (asp *AutoSendPlugin) sendMessageWithRetry(ctx context.Context, task *AutoSendTask) bool {
	maxRetries := 3

	for attempt := 1; attempt <= maxRetries; attempt++ {
		// 解析聊天ID为peer，针对机器人用户使用特殊处理
		peer, err := asp.resolvePeerForTask(ctx, task.ChatID)
		if err != nil {
			logger.Errorf("Attempt %d: Failed to resolve peer for chat %d: %v", attempt, task.ChatID, err)
			if attempt < maxRetries {
				time.Sleep(time.Duration(attempt) * time.Second) // 递增延迟
				continue
			}
			return false
		}

		// 发送消息
		_, err = asp.telegramAPI.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
			Peer:     peer,
			Message:  task.Message,
			RandomID: time.Now().UnixNano(),
		})

		if err != nil {
			errStr := err.Error()
			logger.Errorf("Attempt %d: Failed to send autosend message to chat %d: %v", attempt, task.ChatID, err)

			// 检查是否是可重试的错误
			if asp.isRetryableError(errStr) && attempt < maxRetries {
				logger.Infof("Retryable error detected, waiting before retry...")
				time.Sleep(time.Duration(attempt*2) * time.Second) // 递增延迟
				continue
			}

			// 不可重试的错误或已达到最大重试次数
			return false
		}

		// 成功发送
		return true
	}

	return false
}

// resolvePeerForTask 为任务解析peer，针对机器人用户使用特殊处理
func (asp *AutoSendPlugin) resolvePeerForTask(ctx context.Context, chatID int64) (tg.InputPeerClass, error) {
	// 如果是用户（正数chatID，可能是机器人）
	if chatID > 0 && asp.accessHashManager != nil {
		// 尝试使用AccessHashManager获取正确的AccessHash
		userPeer, err := asp.accessHashManager.GetUserPeerWithFallback(ctx, chatID, nil)
		if err == nil {
			logger.Debugf("Successfully resolved user %d with AccessHashManager", chatID)
			return userPeer, nil
		}

		// 如果AccessHashManager失败，检查是否是失败次数过多
		if strings.Contains(err.Error(), "失败次数过多") {
			logger.Errorf("User %d AccessHash获取失败次数过多，需要重新建立连接", chatID)
			return nil, fmt.Errorf("用户%d的AccessHash已失效，请重新建立连接", chatID)
		}

		logger.Warnf("AccessHashManager failed for user %d: %v, falling back to standard resolver", chatID, err)
	}

	// 回退到标准的peer resolver
	return asp.peerResolver.ResolveFromChatID(ctx, chatID)
}

// isRetryableError 判断错误是否可重试
func (asp *AutoSendPlugin) isRetryableError(errStr string) bool {
	retryableErrors := []string{
		"PEER_ID_INVALID",
		"ACCESS_HASH_INVALID",
		"CHANNEL_INVALID",
		"FLOOD_WAIT",
		"TIMEOUT",
		"network",
		"connection",
	}

	errStrLower := strings.ToLower(errStr)
	for _, retryableErr := range retryableErrors {
		if strings.Contains(errStrLower, strings.ToLower(retryableErr)) {
			return true
		}
	}
	return false
}

// handleFailedTask 处理失败的任务
func (asp *AutoSendPlugin) handleFailedTask(task *AutoSendTask) {
	// 记录失败次数
	logger.Warnf("Task %d failed multiple times, consider checking chat ID %d validity", task.ID, task.ChatID)

	// 如果是用户（正数chatID），清除其AccessHash缓存
	if task.ChatID > 0 && asp.accessHashManager != nil {
		asp.accessHashManager.ClearUserCache(task.ChatID)
		logger.Infof("Cleared AccessHash cache for user %d due to task failure", task.ChatID)
	}

	// 自动禁用连续失败的任务（避免持续错误）
	asp.tasksMutex.Lock()
	defer asp.tasksMutex.Unlock()

	// 从cron调度器移除
	if task.cronID != 0 {
		asp.cronScheduler.Remove(task.cronID)
		task.cronID = 0
	}

	// 更新数据库状态
	_, err := asp.db.Exec("UPDATE autosend_tasks SET enabled = 0 WHERE id = ?", task.ID)
	if err != nil {
		logger.Errorf("Failed to disable failed task %d: %v", task.ID, err)
	} else {
		// 更新内存状态
		task.Enabled = false
		logger.Infof("Auto-disabled failed task %d (chat %d)", task.ID, task.ChatID)
	}
}

// handleAutoSend 处理autosend命令
func (asp *AutoSendPlugin) handleAutoSend(ctx *command.CommandContext) error {
	if len(ctx.Args) == 0 {
		return asp.sendHelp(ctx)
	}

	subcommand := ctx.Args[0]
	switch subcommand {
	case "add", "create":
		return asp.handleAdd(ctx)
	case "list", "ls":
		return asp.handleList(ctx)
	case "remove", "rm", "delete":
		return asp.handleRemove(ctx)
	case "enable":
		return asp.handleEnable(ctx)
	case "disable":
		return asp.handleDisable(ctx)
	case "check":
		return asp.handleCheck(ctx)
	case "resolve":
		return asp.handleResolve(ctx)
	case "clear":
		return asp.handleClear(ctx)
	case "help":
		return asp.sendHelp(ctx)
	default:
		return asp.sendResponse(ctx, "未知子命令: "+subcommand+"\n使用 .autosend help 查看帮助")
	}
}

// handleAdd 处理添加任务
func (asp *AutoSendPlugin) handleAdd(ctx *command.CommandContext) error {
	if len(ctx.Args) < 2 {
		return asp.sendResponse(ctx, "用法: .autosend add <cron表达式> <消息内容>\n例如: .autosend add 0 0 0 * * * 每天0点发送消息\n\nCron表达式格式: 秒 分 时 日 月 周\n常用示例:\n• 0 0 0 * * * - 每天0点\n• 0 30 12 * * * - 每天12:30\n• 0 */10 * * * * - 每10分钟\n\n注意: 不需要使用引号包围cron表达式")
	}

	// 重新组合cron表达式和消息
	// 假设cron表达式是前6个参数，剩余的是消息内容
	if len(ctx.Args) < 7 {
		return asp.sendResponse(ctx, "参数不足。用法: .autosend add <秒> <分> <时> <日> <月> <周> <消息内容>\n例如: .autosend add 0 0 0 * * * 每天0点签到")
	}

	// 构建cron表达式（前6个参数）
	cronFields := ctx.Args[1:7]
	cronExpr := strings.Join(cronFields, " ")

	// 验证cron表达式 - 使用支持秒字段的解析器
	parser := cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	_, err := parser.Parse(cronExpr)
	if err != nil {
		return asp.sendResponse(ctx, "无效的cron表达式: "+err.Error()+"\n\n格式: 秒 分 时 日 月 周\n示例:\n• 0 0 0 * * * - 每天0点\n• 0 30 12 * * * - 每天12:30\n• 0 */10 * * * * - 每10分钟")
	}

	// 组合消息内容（第7个参数开始）
	message := strings.Join(ctx.Args[7:], " ")
	if len(message) == 0 {
		return asp.sendResponse(ctx, "消息内容不能为空")
	}

	// 创建任务
	chatID := ctx.Message.ChatID

	// 计算下次运行时间（用于显示，实际调度由cron管理）
	cronParser := cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	schedule, _ := cronParser.Parse(cronExpr)
	nextRun := schedule.Next(time.Now())

	result, err := asp.db.Exec(`
		INSERT INTO autosend_tasks (chat_id, message, cron_expr, enabled, next_run)
		VALUES (?, ?, ?, 1, ?)
	`, chatID, message, cronExpr, nextRun.Format("2006-01-02 15:04:05"))

	if err != nil {
		return asp.sendResponse(ctx, "创建任务失败: "+err.Error())
	}

	taskID, _ := result.LastInsertId()

	// 创建任务对象
	task := &AutoSendTask{
		ID:       taskID,
		ChatID:   chatID,
		Message:  message,
		CronExpr: cronExpr,
		NextRun:  nextRun,
		Enabled:  true,
		Created:  time.Now(),
	}

	// 添加到cron调度器
	cronID, err := asp.cronScheduler.AddFunc(cronExpr, func() {
		asp.executeTask(task)
	})
	if err != nil {
		// 如果添加到调度器失败，删除数据库记录
		asp.db.Exec("DELETE FROM autosend_tasks WHERE id = ?", taskID)
		return asp.sendResponse(ctx, "添加到调度器失败: "+err.Error())
	}

	task.cronID = cronID

	// 添加到内存中
	asp.tasksMutex.Lock()
	asp.tasks[taskID] = task
	asp.tasksMutex.Unlock()

	chatInfo := asp.getChatInfo(chatID)
	response := fmt.Sprintf("✅ 定时发送任务创建成功！\n"+
		"任务ID: %d\n"+
		"发送到: %s\n"+
		"Cron表达式: %s\n"+
		"消息: %s\n"+
		"下次运行: %s\n"+
		"创建时间: %s",
		taskID, chatInfo, cronExpr, message, nextRun.Format("2006-01-02 15:04:05"), time.Now().Format("2006-01-02 15:04:05"))

	// 发送响应
	err = asp.sendResponse(ctx, response)
	if err != nil {
		return err
	}

	// 15秒后自动删除原始命令消息
	go func() {
		time.Sleep(15 * time.Second)
		asp.deleteMessageWithRetry(ctx)
	}()

	return nil
}

// deleteMessage 删除消息的辅助函数
func (asp *AutoSendPlugin) deleteMessage(ctx *command.CommandContext) {
	deleteCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	logger.Debugf("Attempting to delete command message %d in chat %d", ctx.Message.Message.ID, ctx.Message.ChatID)

	// 解析聊天ID为peer
	peer, err := asp.peerResolver.ResolveFromChatID(deleteCtx, ctx.Message.ChatID)
	if err != nil {
		logger.Debugf("Failed to resolve peer for message deletion: %v", err)
		return
	}

	// 根据聊天类型选择不同的删除方法
	if ctx.Message.ChatID > 0 {
		// 私聊 - 使用普通删除
		_, err = asp.telegramAPI.MessagesDeleteMessages(deleteCtx, &tg.MessagesDeleteMessagesRequest{
			ID: []int{ctx.Message.Message.ID},
		})
	} else {
		// 群聊/频道 - 使用带peer的删除
		_, err = asp.telegramAPI.MessagesDeleteHistory(deleteCtx, &tg.MessagesDeleteHistoryRequest{
			Peer:      peer,
			MaxID:     ctx.Message.Message.ID,
			JustClear: false,
		})
	}

	if err != nil {
		logger.Infof("Failed to delete command message %d: %v", ctx.Message.Message.ID, err)
	} else {
		logger.Infof("Successfully deleted command message %d", ctx.Message.Message.ID)
	}
}

// deleteMessageWithRetry 带重试机制的删除消息函数
func (asp *AutoSendPlugin) deleteMessageWithRetry(ctx *command.CommandContext) {
	logger.Debugf("Starting 15-second countdown to delete command message %d", ctx.Message.Message.ID)

	// 简单检查，如果API不可用则直接跳过
	if asp.telegramAPI == nil || asp.peerResolver == nil {
		logger.Infof("Skipping message deletion: Telegram API not available")
		return
	}

	// 调用原始删除函数
	asp.deleteMessage(ctx)
}

// parseFlexibleTimeString 解析时间字符串，支持多种格式
func (asp *AutoSendPlugin) parseFlexibleTimeString(timeStr string) (time.Time, error) {
	if timeStr == "" {
		return time.Time{}, fmt.Errorf("empty time string")
	}

	// 支持的时间格式列表
	timeFormats := []string{
		"2006-01-02T15:04:05Z",      // ISO 8601 UTC format
		"2006-01-02T15:04:05.000Z",  // ISO 8601 UTC with milliseconds
		"2006-01-02T15:04:05-07:00", // ISO 8601 with timezone
		"2006-01-02 15:04:05",       // Standard SQL format
		"2006-01-02 15:04:05.000",   // SQL with milliseconds
		time.RFC3339,                // RFC3339 format
		time.RFC3339Nano,            // RFC3339 with nanoseconds
	}

	// 尝试每种格式
	for _, format := range timeFormats {
		if t, err := time.Parse(format, timeStr); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("unable to parse time string: %s", timeStr)
}

// handleList 处理列出任务
func (asp *AutoSendPlugin) handleList(ctx *command.CommandContext) error {
	asp.tasksMutex.RLock()
	defer asp.tasksMutex.RUnlock()

	if len(asp.tasks) == 0 {
		return asp.sendResponse(ctx, "当前没有自动发送任务")
	}

	var response strings.Builder
	response.WriteString("📋 自动发送任务列表:\n\n")

	for _, task := range asp.tasks {
		status := "✅ 启用"
		if !task.Enabled {
			status = "❌ 禁用"
		}

		// 获取聊天信息
		chatInfo := asp.getChatInfo(task.ChatID)

		response.WriteString(fmt.Sprintf("ID: %d %s\n", task.ID, status))
		response.WriteString(fmt.Sprintf("发送到: %s\n", chatInfo))
		response.WriteString(fmt.Sprintf("Cron表达式: %s\n", task.CronExpr))
		response.WriteString(fmt.Sprintf("消息: %s\n", task.Message))
		response.WriteString(fmt.Sprintf("下次运行: %s\n", task.NextRun.Format("2006-01-02 15:04:05")))
		response.WriteString(fmt.Sprintf("创建时间: %s\n", task.Created.Format("2006-01-02 15:04:05")))
		response.WriteString("─────────────\n")
	}

	return asp.sendResponse(ctx, response.String())
}

// getChatInfo 获取聊天信息
func (asp *AutoSendPlugin) getChatInfo(chatID int64) string {
	// 如果没有peerResolver，返回chatID
	if asp.peerResolver == nil {
		return fmt.Sprintf("Chat ID: %d", chatID)
	}

	// 尝试解析聊天信息
	ctx := context.Background()
	peer, err := asp.peerResolver.ResolveFromChatID(ctx, chatID)
	if err != nil {
		return fmt.Sprintf("Chat ID: %d", chatID)
	}

	// 根据peer类型返回不同的信息
	switch peer.(type) {
	case *tg.InputPeerUser:
		// 私聊用户
		return fmt.Sprintf("私聊用户 (ID: %d)", chatID)
	case *tg.InputPeerChat:
		// 普通群聊
		return fmt.Sprintf("群聊 (ID: %d)", chatID)
	case *tg.InputPeerChannel:
		// 频道或超级群
		return fmt.Sprintf("频道/超级群 (ID: %d)", chatID)
	default:
		return fmt.Sprintf("Chat ID: %d", chatID)
	}
}

// handleRemove 处理删除任务
func (asp *AutoSendPlugin) handleRemove(ctx *command.CommandContext) error {
	if len(ctx.Args) < 2 {
		return asp.sendResponse(ctx, "用法: .autosend remove <任务ID>")
	}

	taskID, err := strconv.ParseInt(ctx.Args[1], 10, 64)
	if err != nil {
		return asp.sendResponse(ctx, "无效的任务ID")
	}

	asp.tasksMutex.Lock()
	defer asp.tasksMutex.Unlock()

	task, exists := asp.tasks[taskID]
	if !exists {
		return asp.sendResponse(ctx, "任务不存在")
	}

	// 从cron调度器删除
	if task.cronID != 0 {
		asp.cronScheduler.Remove(task.cronID)
	}

	// 从数据库删除
	_, err = asp.db.Exec("DELETE FROM autosend_tasks WHERE id = ?", taskID)
	if err != nil {
		return asp.sendResponse(ctx, "删除任务失败: "+err.Error())
	}

	// 从内存删除
	delete(asp.tasks, taskID)

	// 发送响应
	err = asp.sendResponse(ctx, fmt.Sprintf("✅ 任务 %d 已删除", taskID))
	if err != nil {
		return err
	}

	// 15秒后自动删除原始命令消息
	go func() {
		time.Sleep(15 * time.Second)
		asp.deleteMessageWithRetry(ctx)
	}()

	return nil
}

// handleEnable 处理启用任务
func (asp *AutoSendPlugin) handleEnable(ctx *command.CommandContext) error {
	if len(ctx.Args) < 2 {
		return asp.sendResponse(ctx, "用法: .autosend enable <任务ID>")
	}

	taskID, err := strconv.ParseInt(ctx.Args[1], 10, 64)
	if err != nil {
		return asp.sendResponse(ctx, "无效的任务ID")
	}

	asp.tasksMutex.Lock()
	defer asp.tasksMutex.Unlock()

	task, exists := asp.tasks[taskID]
	if !exists {
		return asp.sendResponse(ctx, "任务不存在")
	}

	if task.Enabled {
		return asp.sendResponse(ctx, "任务已经是启用状态")
	}

	// 更新数据库
	_, err = asp.db.Exec("UPDATE autosend_tasks SET enabled = 1 WHERE id = ?", taskID)
	if err != nil {
		return asp.sendResponse(ctx, "启用任务失败: "+err.Error())
	}

	// 重新添加到cron调度器
	cronID, err := asp.cronScheduler.AddFunc(task.CronExpr, func() {
		asp.executeTask(task)
	})
	if err != nil {
		return asp.sendResponse(ctx, "重新添加到调度器失败: "+err.Error())
	}

	// 更新内存
	task.Enabled = true
	task.cronID = cronID

	// 发送响应
	err = asp.sendResponse(ctx, fmt.Sprintf("✅ 任务 %d 已启用", taskID))
	if err != nil {
		return err
	}

	// 15秒后自动删除原始命令消息
	go func() {
		time.Sleep(15 * time.Second)
		asp.deleteMessageWithRetry(ctx)
	}()

	return nil
}

// handleDisable 处理禁用任务
func (asp *AutoSendPlugin) handleDisable(ctx *command.CommandContext) error {
	if len(ctx.Args) < 2 {
		return asp.sendResponse(ctx, "用法: .autosend disable <任务ID>")
	}

	taskID, err := strconv.ParseInt(ctx.Args[1], 10, 64)
	if err != nil {
		return asp.sendResponse(ctx, "无效的任务ID")
	}

	asp.tasksMutex.Lock()
	defer asp.tasksMutex.Unlock()

	task, exists := asp.tasks[taskID]
	if !exists {
		return asp.sendResponse(ctx, "任务不存在")
	}

	if !task.Enabled {
		return asp.sendResponse(ctx, "任务已经是禁用状态")
	}

	// 从cron调度器移除
	if task.cronID != 0 {
		asp.cronScheduler.Remove(task.cronID)
		task.cronID = 0
	}

	// 更新数据库
	_, err = asp.db.Exec("UPDATE autosend_tasks SET enabled = 0 WHERE id = ?", taskID)
	if err != nil {
		return asp.sendResponse(ctx, "禁用任务失败: "+err.Error())
	}

	// 更新内存
	task.Enabled = false

	// 发送响应
	err = asp.sendResponse(ctx, fmt.Sprintf("✅ 任务 %d 已禁用", taskID))
	if err != nil {
		return err
	}

	// 15秒后自动删除原始命令消息
	go func() {
		time.Sleep(15 * time.Second)
		asp.deleteMessageWithRetry(ctx)
	}()

	return nil
}

// handleCheck 处理检查任务有效性
func (asp *AutoSendPlugin) handleCheck(ctx *command.CommandContext) error {
	asp.tasksMutex.RLock()
	defer asp.tasksMutex.RUnlock()

	if len(asp.tasks) == 0 {
		return asp.sendResponse(ctx, "当前没有自动发送任务需要检查")
	}

	var response strings.Builder
	response.WriteString("🔍 检查任务有效性结果:\n\n")

	validTasks := 0
	invalidTasks := 0
	checkCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	for _, task := range asp.tasks {
		status := "✅ 有效"
		chatInfo := asp.getChatInfo(task.ChatID)
		accessHashStatus := ""

		// 尝试解析peer来检查任务是否有效
		if asp.peerResolver != nil {
			_, err := asp.peerResolver.ResolveFromChatID(checkCtx, task.ChatID)
			if err != nil {
				status = "❌ 无效 - " + err.Error()
				invalidTasks++

				// 如果是用户，检查AccessHash状态
				if task.ChatID > 0 && asp.accessHashManager != nil {
					failureCount := asp.accessHashManager.getFailureCount(task.ChatID)
					if failureCount > 0 {
						accessHashStatus = fmt.Sprintf(" (AccessHash失败%d次)", failureCount)
					}
				}
			} else {
				validTasks++
			}
		} else {
			status = "⚠️ 无法检查 - peer resolver 不可用"
		}

		response.WriteString(fmt.Sprintf("ID: %d\n", task.ID))
		response.WriteString(fmt.Sprintf("状态: %s%s\n", status, accessHashStatus))
		response.WriteString(fmt.Sprintf("聊天: %s\n", chatInfo))
		response.WriteString(fmt.Sprintf("消息: %s\n", task.Message))
		response.WriteString("─────────────\n")
	}

	response.WriteString("\n📊 统计:\n")
	response.WriteString(fmt.Sprintf("• 有效任务: %d\n", validTasks))
	response.WriteString(fmt.Sprintf("• 无效任务: %d\n", invalidTasks))
	response.WriteString(fmt.Sprintf("• 总任务数: %d\n", len(asp.tasks)))

	if invalidTasks > 0 {
		response.WriteString("\n💡 建议:\n")
		response.WriteString("• 使用 .autosend remove <ID> 删除无效任务\n")
		response.WriteString("• 检查聊天是否仍然存在或您是否仍在其中\n")
		response.WriteString("• 对于AccessHash失效的用户，使用 .autosend clear <用户ID> 清除缓存\n")
		response.WriteString("• 重新发送消息给该用户/机器人，然后使用 .autosend resolve <用户ID>\n")
	}

	return asp.sendResponse(ctx, response.String())
}

// handleResolve 处理解析用户/机器人命令
func (asp *AutoSendPlugin) handleResolve(ctx *command.CommandContext) error {
	if len(ctx.Args) < 2 {
		return asp.sendResponse(ctx, "用法: .autosend resolve <用户ID>\n例如: .autosend resolve 7626887601")
	}

	userIDStr := ctx.Args[1]
	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		return asp.sendResponse(ctx, "无效的用户ID: "+userIDStr)
	}

	if asp.accessHashManager == nil {
		return asp.sendResponse(ctx, "AccessHashManager 未初始化")
	}

	var response strings.Builder
	response.WriteString(fmt.Sprintf("🔍 尝试解析用户 %d:\n\n", userID))

	resolveCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 尝试获取用户信息
	userPeer, err := asp.accessHashManager.GetUserPeerWithFallback(resolveCtx, userID, nil)
	if err != nil {
		response.WriteString(fmt.Sprintf("❌ 解析失败: %v\n\n", err))

		// 提供建议
		response.WriteString("💡 可能的解决方案:\n")
		response.WriteString("1. 确保您与该用户/机器人有过对话\n")
		response.WriteString("2. 尝试先发送一条消息给该机器人\n")
		response.WriteString("3. 检查用户ID是否正确\n")
		response.WriteString("4. 该用户可能已删除账户或阻止了您\n\n")

		// 尝试提供一个简单的交互方法
		response.WriteString("🤖 如果这是一个机器人，您可以：\n")
		response.WriteString("• 在Telegram中搜索并打开与机器人的对话\n")
		response.WriteString("• 发送 /start 命令给机器人\n")
		response.WriteString("• 然后重新尝试创建 autosend 任务\n")
	} else {
		// userPeer 已经是 *tg.InputPeerUser 类型，因为 GetUserPeerWithFallback 返回该类型
		inputUser := userPeer
		if inputUser != nil {
			response.WriteString("✅ 解析成功!\n")
			response.WriteString(fmt.Sprintf("用户ID: %d\n", inputUser.UserID))
			response.WriteString(fmt.Sprintf("AccessHash: %d\n\n", inputUser.AccessHash))

			// 检查缓存信息
			if userInfo := asp.accessHashManager.GetCachedUserInfo(userID); userInfo != nil {
				response.WriteString("📋 缓存信息:\n")
				if userInfo.Username != "" {
					response.WriteString(fmt.Sprintf("用户名: @%s\n", userInfo.Username))
				}
				if userInfo.FirstName != "" {
					response.WriteString(fmt.Sprintf("名字: %s", userInfo.FirstName))
					if userInfo.LastName != "" {
						response.WriteString(fmt.Sprintf(" %s", userInfo.LastName))
					}
					response.WriteString("\n")
				}
				response.WriteString(fmt.Sprintf("缓存时间: %s\n\n", userInfo.UpdatedAt.Format("2006-01-02 15:04:05")))
			}

			response.WriteString("✅ 现在您可以正常创建 autosend 任务了！")
		} else {
			response.WriteString("⚠️ 解析成功，但返回了非用户类型的peer\n")
		}
	}

	return asp.sendResponse(ctx, response.String())
}

// handleClear 处理清理AccessHash缓存命令
func (asp *AutoSendPlugin) handleClear(ctx *command.CommandContext) error {
	if len(ctx.Args) < 2 {
		return asp.sendResponse(ctx, "用法: .autosend clear <用户ID>\n例如: .autosend clear 7626887601\n\n这将清除指定用户的AccessHash缓存，强制重新获取")
	}

	userIDStr := ctx.Args[1]
	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		return asp.sendResponse(ctx, "无效的用户ID: "+userIDStr)
	}

	if asp.accessHashManager == nil {
		return asp.sendResponse(ctx, "AccessHashManager 未初始化")
	}

	// 清除指定用户的缓存
	asp.accessHashManager.ClearUserCache(userID)

	// 检查是否有相关的autosend任务
	asp.tasksMutex.RLock()
	var relatedTasks []int64
	for taskID, task := range asp.tasks {
		if task.ChatID == userID {
			relatedTasks = append(relatedTasks, taskID)
		}
	}
	asp.tasksMutex.RUnlock()

	response := fmt.Sprintf("✅ 已清除用户 %d 的AccessHash缓存\n\n", userID)

	if len(relatedTasks) > 0 {
		response += fmt.Sprintf("📋 发现 %d 个相关任务:\n", len(relatedTasks))
		for _, taskID := range relatedTasks {
			response += fmt.Sprintf("• 任务ID: %d\n", taskID)
		}
		response += "\n💡 建议:\n"
		response += "• 重新发送一条消息给该用户/机器人\n"
		response += "• 然后使用 .autosend resolve <用户ID> 重新解析\n"
		response += "• 或者重新创建相关任务\n"
	} else {
		response += "💡 建议:\n"
		response += "• 重新发送一条消息给该用户/机器人\n"
		response += "• 然后使用 .autosend resolve <用户ID> 重新解析\n"
	}

	return asp.sendResponse(ctx, response)
}

// sendHelp 发送帮助信息
func (asp *AutoSendPlugin) sendHelp(ctx *command.CommandContext) error {
	helpMsg := `🤖 AutoSend 定时发送插件帮助

📝 基本命令:
• .autosend add <秒> <分> <时> <日> <月> <周> <消息内容> - 创建定时发送任务
• .autosend list - 列出所有任务
• .autosend remove <ID> - 删除任务
• .autosend enable <ID> - 启用任务
• .autosend disable <ID> - 禁用任务
• .autosend check - 检查所有任务的有效性
• .autosend resolve <用户ID> - 解析用户/机器人的AccessHash
• .autosend clear <用户ID> - 清除用户AccessHash缓存

📋 Cron表达式格式: 秒 分 时 日 月 周
• 每天0点: 0 0 0 * * *
• 每天12:30: 0 30 12 * * *  
• 每10分钟: 0 */10 * * * *
• 每小时: 0 0 * * * *
• 工作日9点: 0 0 9 * * 1-5
• 每周日22点: 0 0 22 * * 0

📋 使用示例:
• .autosend add 0 0 0 * * * 🌅 新的一天开始了！
• .autosend add 0 30 12 * * * 🍽️ 午餐时间到了！
• .autosend add 0 0 22 * * * 🌙 该休息了，晚安~
• .as add 0 */30 * * * * 📊 半小时状态检查
• .autosend add 0 0 9 * * 1-5 ☕ 工作日早安！
• .autosend list - 查看所有任务
• .autosend remove 1 - 删除ID为1的任务

⚠️ 注意事项:
• 使用标准cron表达式，支持秒级精度
• 无需使用引号，直接输入6个字段
• 消息内容完全自定义，支持emoji、换行等
• 任务会在当前聊天中执行
• 重启后任务会自动恢复
• 使用.as作为简写命令

🔧 故障排除:
• 如果任务失败显示"PEER_ID_INVALID"，说明AccessHash已失效
• 使用 .autosend clear <用户ID> 清除缓存
• 重新发送消息给该用户/机器人
• 使用 .autosend resolve <用户ID> 重新解析
• 任务连续失败3次会自动禁用

🔌 插件信息:
• 名称: autosend
• 版本: v1.0.0
• 描述: 基于cron表达式的定时自动发送消息插件`

	return asp.sendResponse(ctx, helpMsg)
}

// sendResponse 发送响应消息
func (asp *AutoSendPlugin) sendResponse(ctx *command.CommandContext, message string) error {
	peer, err := ctx.PeerResolver.ResolveFromChatID(ctx.Context, ctx.Message.ChatID)
	if err != nil {
		return fmt.Errorf("failed to resolve peer: %w", err)
	}

	// 私聊：编辑消息，群聊：先尝试编辑，失败则发送新消息
	if ctx.Message.ChatID > 0 {
		_, err = ctx.API.MessagesEditMessage(ctx.Context, &tg.MessagesEditMessageRequest{
			Peer:    peer,
			ID:      ctx.Message.Message.ID,
			Message: message,
		})
		return err
	} else {
		_, err = ctx.API.MessagesEditMessage(ctx.Context, &tg.MessagesEditMessageRequest{
			Peer:    peer,
			ID:      ctx.Message.Message.ID,
			Message: message,
		})
		if err != nil {
			_, err = ctx.API.MessagesSendMessage(ctx.Context, &tg.MessagesSendMessageRequest{
				Peer:     peer,
				Message:  message,
				RandomID: time.Now().UnixNano(),
			})
		}
		return err
	}
}
