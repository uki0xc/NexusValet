package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"nexusvalet/internal/command"
	"nexusvalet/pkg/logger"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
)

// SpeedTestPlugin 网速测试插件
type SpeedTestPlugin struct {
	*BasePlugin
	speedtestPath string
}

// SpeedTestResult 测速结果结构体
type SpeedTestResult struct {
	Server struct {
		Name     string      `json:"name"`
		ID       interface{} `json:"id"` // 可能是string或number
		Location string      `json:"location"`
	} `json:"server"`
	Upload struct {
		Bandwidth int64 `json:"bandwidth"`
	} `json:"upload"`
	Download struct {
		Bandwidth int64 `json:"bandwidth"`
	} `json:"download"`
	Ping struct {
		Latency float64 `json:"latency"`
	} `json:"ping"`
	Timestamp string `json:"timestamp"`
	Result    struct {
		URL string `json:"url"`
	} `json:"result"`
}

// SpeedTestServers 服务器列表结构体
type SpeedTestServers struct {
	Servers []struct {
		ID       interface{} `json:"id"` // 可能是string或number
		Name     string      `json:"name"`
		Location string      `json:"location"`
	} `json:"servers"`
}

// NewSpeedTestPlugin 创建网速测试插件
func NewSpeedTestPlugin() *SpeedTestPlugin {
	info := &PluginInfo{
		PluginVersion: &PluginVersion{
			Name:        "speedtest",
			Version:     "1.0.0",
			Author:      "NexusValet",
			Description: "网络速度测试插件，基于Ookla Speedtest CLI",
		},
		Dir:     "builtin",
		Enabled: true,
	}

	plugin := &SpeedTestPlugin{
		BasePlugin:    NewBasePlugin(info),
		speedtestPath: "/tmp/nexusvalet/speedtest",
	}

	return plugin
}

// RegisterCommands 实现CommandPlugin接口
func (st *SpeedTestPlugin) RegisterCommands(parser *command.Parser) error {
	parser.RegisterCommand("st", "网络速度测试", st.info.Name, st.handleSpeedTest)
	logger.Infof("SpeedTest plugin commands registered successfully")
	return nil
}

// sendResponse SpeedTest插件通用响应函数
func (st *SpeedTestPlugin) sendResponse(ctx *command.CommandContext, message string) error {
	peer, err := ctx.PeerResolver.ResolveFromChatID(ctx.Context, ctx.Message.ChatID)
	if err != nil {
		return fmt.Errorf("failed to resolve peer: %w", err)
	}

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

// editWithPhoto 编辑消息为图片
func (st *SpeedTestPlugin) editWithPhoto(ctx *command.CommandContext, imagePath string, caption string) error {
	peer, err := ctx.PeerResolver.ResolveFromChatID(ctx.Context, ctx.Message.ChatID)
	if err != nil {
		return fmt.Errorf("failed to resolve peer: %w", err)
	}

	// 读取图片文件
	imageData, err := os.ReadFile(imagePath)
	if err != nil {
		return fmt.Errorf("failed to read image file: %w", err)
	}

	// 上传图片文件
	uploader := uploader.NewUploader(ctx.API)
	file, err := uploader.FromBytes(ctx.Context, fmt.Sprintf("speedtest_%d.png", time.Now().Unix()), imageData)
	if err != nil {
		return fmt.Errorf("failed to upload image: %w", err)
	}

	// 编辑消息为图片
	_, err = ctx.API.MessagesEditMessage(ctx.Context, &tg.MessagesEditMessageRequest{
		Peer: peer,
		ID:   ctx.Message.Message.ID,
		Media: &tg.InputMediaUploadedPhoto{
			File: file,
		},
		Message: caption,
	})

	if err != nil {
		return fmt.Errorf("failed to edit message with photo: %w", err)
	}

	logger.Infof("Successfully edited message %d with photo in chatID=%d", ctx.Message.Message.ID, ctx.Message.ChatID)
	return nil
}

// handleSpeedTest 处理网速测试命令
func (st *SpeedTestPlugin) handleSpeedTest(ctx *command.CommandContext) error {
	// 检查参数
	if len(ctx.Args) > 0 && ctx.Args[0] == "list" {
		return st.handleListServers(ctx)
	}

	// 开始测速
	st.sendResponse(ctx, "🚀 开始网速测试，请稍候...")

	// 确保speedtest CLI存在
	if err := st.ensureSpeedTestCLI(); err != nil {
		return st.sendResponse(ctx, fmt.Sprintf("❌ 初始化测速工具失败: %v", err))
	}

	// 构建命令
	var serverID string
	if len(ctx.Args) > 0 && st.isDigit(ctx.Args[0]) {
		serverID = ctx.Args[0]
	}

	result, err := st.runSpeedTest(serverID)
	if err != nil {
		return st.sendResponse(ctx, fmt.Sprintf("❌ 测速失败: %v", err))
	}

	// 格式化结果
	response := st.formatResult(result)

	// 如果有结果URL，尝试下载并发送图片
	if result.Result.URL != "" {
		imagePath, err := st.downloadAndCropImage(result.Result.URL)
		if err != nil {
			logger.Debugf("下载图片失败: %v", err)
			// 如果图片下载失败，只发送文字结果
			return st.sendResponse(ctx, response)
		}
		defer os.Remove(imagePath) // 发送后清理图片文件

		// 编辑原消息为图片和文字说明
		err = st.editWithPhoto(ctx, imagePath, response)
		if err != nil {
			logger.Errorf("编辑消息为图片失败: %v", err)
			// 如果图片编辑失败，编辑为文字结果
			return st.sendResponse(ctx, response)
		}

		logger.Infof("成功编辑消息为测速结果图片")
		return nil
	}

	return st.sendResponse(ctx, response)
}

// handleListServers 处理列出服务器命令
func (st *SpeedTestPlugin) handleListServers(ctx *command.CommandContext) error {
	st.sendResponse(ctx, "🔍 获取附近的测速服务器...")

	if err := st.ensureSpeedTestCLI(); err != nil {
		return st.sendResponse(ctx, fmt.Sprintf("❌ 初始化测速工具失败: %v", err))
	}

	servers, err := st.getServerList()
	if err != nil {
		return st.sendResponse(ctx, fmt.Sprintf("❌ 获取服务器列表失败: %v", err))
	}

	if len(servers.Servers) == 0 {
		return st.sendResponse(ctx, "📍 附近没有找到测速服务器")
	}

	var response strings.Builder
	response.WriteString("📡 附近的测速服务器:\n\n")

	// 只显示前10个服务器，避免消息过长
	maxServers := len(servers.Servers)
	if maxServers > 10 {
		maxServers = 10
	}

	for i := 0; i < maxServers; i++ {
		server := servers.Servers[i]
		// 处理服务器ID，可能是字符串或数字
		var serverID string
		switch id := server.ID.(type) {
		case string:
			serverID = id
		case float64:
			serverID = fmt.Sprintf("%.0f", id)
		case int:
			serverID = fmt.Sprintf("%d", id)
		default:
			serverID = fmt.Sprintf("%v", id)
		}
		response.WriteString(fmt.Sprintf("• `%s` - %s - %s\n",
			serverID, server.Name, server.Location))
	}

	if len(servers.Servers) > 10 {
		response.WriteString(fmt.Sprintf("\n... 还有 %d 个服务器\n", len(servers.Servers)-10))
	}

	response.WriteString("\n💡 使用 `.st <服务器ID>` 指定服务器测速")

	return st.sendResponse(ctx, response.String())
}

// ensureSpeedTestCLI 确保speedtest CLI存在
func (st *SpeedTestPlugin) ensureSpeedTestCLI() error {
	// 检查是否已存在
	if _, err := os.Stat(st.speedtestPath); err == nil {
		return nil
	}

	logger.Infof("Downloading Speedtest CLI...")

	// 创建目录
	dir := filepath.Dir(st.speedtestPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	// 根据系统架构下载对应版本
	downloadURL, err := st.getDownloadURL()
	if err != nil {
		return err
	}

	// 下载并安装
	if err := st.downloadAndInstall(downloadURL); err != nil {
		return err
	}

	// 设置执行权限
	if err := os.Chmod(st.speedtestPath, 0755); err != nil {
		return fmt.Errorf("设置执行权限失败: %w", err)
	}

	logger.Infof("Speedtest CLI installed successfully")
	return nil
}

// getDownloadURL 获取下载URL
func (st *SpeedTestPlugin) getDownloadURL() (string, error) {
	version := "1.2.0"
	machine := runtime.GOARCH

	// 映射架构名称
	switch machine {
	case "amd64":
		machine = "x86_64"
	case "arm64":
		machine = "aarch64"
	}

	var osName string
	switch runtime.GOOS {
	case "linux":
		osName = "linux"
	case "darwin":
		osName = "macosx"
	case "windows":
		osName = "win64"
	default:
		return "", fmt.Errorf("不支持的操作系统: %s", runtime.GOOS)
	}

	filename := fmt.Sprintf("ookla-speedtest-%s-%s-%s.tgz", version, osName, machine)
	url := fmt.Sprintf("https://install.speedtest.net/app/cli/%s", filename)

	return url, nil
}

// downloadAndInstall 下载并安装
func (st *SpeedTestPlugin) downloadAndInstall(url string) error {
	// 下载文件
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载失败，状态码: %d", resp.StatusCode)
	}

	// 保存到临时文件
	tmpFile := st.speedtestPath + ".tgz"
	file, err := os.Create(tmpFile)
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	defer file.Close()
	defer os.Remove(tmpFile)

	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return fmt.Errorf("保存文件失败: %w", err)
	}

	// 解压文件
	if err := st.extractTarGz(tmpFile, filepath.Dir(st.speedtestPath)); err != nil {
		return fmt.Errorf("解压失败: %w", err)
	}

	return nil
}

// extractTarGz 解压tar.gz文件
func (st *SpeedTestPlugin) extractTarGz(src, dest string) error {
	// 使用系统命令解压
	cmd := exec.Command("tar", "-xzf", src, "-C", dest)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("解压命令失败: %w", err)
	}

	return nil
}

// runSpeedTest 运行速度测试
func (st *SpeedTestPlugin) runSpeedTest(serverID string) (*SpeedTestResult, error) {
	args := []string{"--accept-license", "--accept-gdpr", "-f", "json"}
	if serverID != "" {
		args = append(args, "-s", serverID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, st.speedtestPath, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("执行测速命令失败: %w", err)
	}

	var result SpeedTestResult
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("解析测速结果失败: %w", err)
	}

	return &result, nil
}

// getServerList 获取服务器列表
func (st *SpeedTestPlugin) getServerList() (*SpeedTestServers, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, st.speedtestPath, "-f", "json", "-L")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("获取服务器列表失败: %w", err)
	}

	var servers SpeedTestServers
	if err := json.Unmarshal(output, &servers); err != nil {
		return nil, fmt.Errorf("解析服务器列表失败: %w", err)
	}

	return &servers, nil
}

// formatResult 格式化测速结果
func (st *SpeedTestPlugin) formatResult(result *SpeedTestResult) string {
	uploadSpeed := st.unitConvert(result.Upload.Bandwidth)
	downloadSpeed := st.unitConvert(result.Download.Bandwidth)

	// 处理服务器ID，可能是字符串或数字
	var serverID string
	switch id := result.Server.ID.(type) {
	case string:
		serverID = id
	case float64:
		serverID = fmt.Sprintf("%.0f", id)
	case int:
		serverID = fmt.Sprintf("%d", id)
	default:
		serverID = fmt.Sprintf("%v", id)
	}

	response := fmt.Sprintf(`** 逆旅之人，终有归期 **
刽子手拔刀斋: `+"%s - %s"+`
剑心所居: `+"%s"+`
飞天御剑流: `+"%s"+`
神速居合斩: `+"%s"+`
九头龙闪: `+"%.2f ms"+`
幕末之刻: `+"%s",
		result.Server.Name,
		serverID,
		result.Server.Location,
		uploadSpeed,
		downloadSpeed,
		result.Ping.Latency,
		result.Timestamp)

	if result.Result.URL != "" {
		response += fmt.Sprintf("\n无明神风流: %s", result.Result.URL)
	}

	return response
}

// unitConvert 转换带宽单位
func (st *SpeedTestPlugin) unitConvert(bandwidth int64) string {
	// bandwidth是以bits per second为单位
	bps := float64(bandwidth)

	units := []string{"bps", "Kbps", "Mbps", "Gbps", "Tbps"}
	unitIndex := 0

	for bps >= 1000 && unitIndex < len(units)-1 {
		bps /= 1000
		unitIndex++
	}

	return fmt.Sprintf("%.2f %s", bps, units[unitIndex])
}

// isDigit 检查字符串是否为数字
func (st *SpeedTestPlugin) isDigit(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

// downloadAndCropImage 下载并裁剪测速结果图片
func (st *SpeedTestPlugin) downloadAndCropImage(resultURL string) (string, error) {
	// 下载图片
	imageURL := resultURL + ".png"
	resp, err := http.Get(imageURL)
	if err != nil {
		return "", fmt.Errorf("下载图片失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载图片失败，状态码: %d", resp.StatusCode)
	}

	// 创建临时文件
	tmpDir := filepath.Dir(st.speedtestPath)
	imagePath := filepath.Join(tmpDir, "speedtest_result.png")

	// 保存原始图片
	file, err := os.Create(imagePath)
	if err != nil {
		return "", fmt.Errorf("创建图片文件失败: %w", err)
	}
	defer file.Close()

	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return "", fmt.Errorf("保存图片失败: %w", err)
	}

	// 裁剪图片 (类似Python版本的裁剪: c = img.crop((17, 11, 727, 389)))
	if err := st.cropImage(imagePath); err != nil {
		logger.Debugf("裁剪图片失败: %v", err)
		// 裁剪失败不影响图片发送，继续使用原图
	}

	return imagePath, nil
}

// cropImage 裁剪图片
func (st *SpeedTestPlugin) cropImage(imagePath string) error {
	// 打开图片
	file, err := os.Open(imagePath)
	if err != nil {
		return fmt.Errorf("打开图片失败: %w", err)
	}
	defer file.Close()

	// 解码图片
	img, _, err := image.Decode(file)
	if err != nil {
		return fmt.Errorf("解码图片失败: %w", err)
	}

	// 获取图片尺寸
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	// 计算裁剪区域 (类似Python版本: 17, 11, 727, 389)
	cropX1 := 17
	cropY1 := 11
	cropX2 := 727
	cropY2 := 389

	// 确保裁剪区域不超出图片边界
	if cropX2 > width {
		cropX2 = width
	}
	if cropY2 > height {
		cropY2 = height
	}

	// 创建裁剪区域
	cropRect := image.Rect(cropX1, cropY1, cropX2, cropY2)

	// 裁剪图片
	croppedImg := img.(interface {
		SubImage(r image.Rectangle) image.Image
	}).SubImage(cropRect)

	// 保存裁剪后的图片
	outFile, err := os.Create(imagePath)
	if err != nil {
		return fmt.Errorf("创建输出文件失败: %w", err)
	}
	defer outFile.Close()

	// 编码并保存
	if err := png.Encode(outFile, croppedImg); err != nil {
		return fmt.Errorf("保存裁剪图片失败: %w", err)
	}

	return nil
}
