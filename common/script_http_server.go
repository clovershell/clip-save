package common

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	httpServer          *http.Server
	httpServerMutex     sync.RWMutex
	enabledScripts      = make(map[string]*UserScript) // identifier -> script
	enabledScriptsMutex sync.RWMutex
	scriptResults       = make(map[string]chan ScriptHTTPResult) // requestID -> result channel
	scriptResultsMutex  sync.RWMutex
	requestCounter      int64
	requestCounterMutex sync.Mutex
	cleanupTicker       *time.Ticker
	cleanupStopChan     chan struct{}
)

// ScriptHTTPResult 脚本执行结果
type ScriptHTTPResult struct {
	ReturnValue interface{} `json:"returnValue,omitempty"`
	Error       string      `json:"error,omitempty"`
}

// GetScriptIdentifier 获取脚本的 HTTP 服务标识符
func GetScriptIdentifier(script *UserScript) string {
	// 优先使用 plugin_id
	if script.PluginID != "" {
		return script.PluginID
	}

	// 如果没有 plugin_id，从 ID 的第 7 位开始取 8 位
	if len(script.ID) >= 15 {
		return script.ID[6:14] // 从索引 6 开始取 8 位（第 7 位到第 14 位）
	}

	// 如果 ID 长度不够，使用整个 ID（去掉前 6 位）
	if len(script.ID) > 6 {
		return script.ID[6:]
	}

	// 如果 ID 太短，直接使用整个 ID
	return script.ID
}

// StartScriptHTTPServer 启动脚本 HTTP 服务器
func StartScriptHTTPServer() error {
	httpServerMutex.Lock()
	defer httpServerMutex.Unlock()

	if httpServer != nil {
		return fmt.Errorf("HTTP 服务器已在运行")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/clip-save/", handleScriptHTTPRequest)

	httpServer = &http.Server{
		Addr:    ":6527",
		Handler: mux,
	}

	// 启动定期清理任务（每 5 分钟清理一次超时的结果通道）
	cleanupStopChan = make(chan struct{})
	cleanupTicker = time.NewTicker(5 * time.Minute)
	go cleanupExpiredResults()

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("❌ 脚本 HTTP 服务器启动失败: %v", err)
		}
	}()

	log.Printf("✅ 脚本 HTTP 服务器已启动，端口: 6527")
	return nil
}

// StopScriptHTTPServer 停止脚本 HTTP 服务器
func StopScriptHTTPServer() error {
	httpServerMutex.Lock()
	defer httpServerMutex.Unlock()

	if httpServer == nil {
		return nil
	}

	// 停止清理任务
	if cleanupTicker != nil {
		cleanupTicker.Stop()
		cleanupTicker = nil
	}
	if cleanupStopChan != nil {
		close(cleanupStopChan)
		cleanupStopChan = nil
	}

	if err := httpServer.Close(); err != nil {
		return fmt.Errorf("停止 HTTP 服务器失败: %v", err)
	}

	// 清理所有结果通道
	scriptResultsMutex.Lock()
	for requestID, resultChan := range scriptResults {
		close(resultChan)
		delete(scriptResults, requestID)
	}
	scriptResultsMutex.Unlock()

	httpServer = nil
	log.Printf("✅ 脚本 HTTP 服务器已停止")
	return nil
}

// cleanupExpiredResults 定期清理超时的结果通道（防止内存泄漏）
func cleanupExpiredResults() {
	for {
		select {
		case <-cleanupStopChan:
			return
		case <-cleanupTicker.C:
			scriptResultsMutex.Lock()
			now := time.Now().Unix()
			expiredCount := 0
			for requestID, resultChan := range scriptResults {
				// 解析 requestID 中的时间戳（格式：req_<timestamp>_<counter>）
				// 如果超过 60 秒，认为是过期请求
				parts := strings.Split(requestID, "_")
				if len(parts) >= 2 {
					var timestamp int64
					if _, err := fmt.Sscanf(parts[1], "%d", &timestamp); err == nil {
						if now-timestamp > 60 {
							close(resultChan)
							delete(scriptResults, requestID)
							expiredCount++
						}
					}
				}
			}
			scriptResultsMutex.Unlock()
			if expiredCount > 0 {
				log.Printf("🧹 清理了 %d 个过期的脚本执行结果通道", expiredCount)
			}
		}
	}
}

// EnableScriptHTTPService 启用脚本的 HTTP 服务
func EnableScriptHTTPService(scriptID string) error {
	script, err := GetUserScriptByID(scriptID)
	if err != nil {
		return fmt.Errorf("获取脚本失败: %v", err)
	}

	identifier := GetScriptIdentifier(script)

	enabledScriptsMutex.Lock()
	defer enabledScriptsMutex.Unlock()

	enabledScripts[identifier] = script

	// 如果服务器未启动，启动它
	httpServerMutex.RLock()
	serverRunning := httpServer != nil
	httpServerMutex.RUnlock()

	if !serverRunning {
		if err := StartScriptHTTPServer(); err != nil {
			return fmt.Errorf("启动 HTTP 服务器失败: %v", err)
		}
	}

	log.Printf("✅ 脚本 HTTP 服务已启用: %s -> /clip-save/%s", script.Name, identifier)
	return nil
}

// DisableScriptHTTPService 禁用脚本的 HTTP 服务
func DisableScriptHTTPService(scriptID string) error {
	script, err := GetUserScriptByID(scriptID)
	if err != nil {
		return fmt.Errorf("获取脚本失败: %v", err)
	}

	identifier := GetScriptIdentifier(script)

	enabledScriptsMutex.Lock()
	defer enabledScriptsMutex.Unlock()

	delete(enabledScripts, identifier)

	log.Printf("✅ 脚本 HTTP 服务已禁用: %s -> /clip-save/%s", script.Name, identifier)
	return nil
}

// IsScriptHTTPServiceEnabled 检查脚本的 HTTP 服务是否已启用
func IsScriptHTTPServiceEnabled(scriptID string) bool {
	script, err := GetUserScriptByID(scriptID)
	if err != nil {
		return false
	}

	identifier := GetScriptIdentifier(script)

	enabledScriptsMutex.RLock()
	defer enabledScriptsMutex.RUnlock()

	_, exists := enabledScripts[identifier]
	return exists
}

// GetScriptHTTPURL 获取脚本的 HTTP 服务 URL
func GetScriptHTTPURL(scriptID string) (string, error) {
	script, err := GetUserScriptByID(scriptID)
	if err != nil {
		return "", fmt.Errorf("获取脚本失败: %v", err)
	}

	identifier := GetScriptIdentifier(script)

	// 获取本机局域网 IP
	ip, err := getLocalIP()
	if err != nil {
		return "", fmt.Errorf("获取本机 IP 失败: %v", err)
	}

	return fmt.Sprintf("http://%s:6527/clip-save/%s?content=xx", ip, identifier), nil
}

// getLocalIP 获取本机局域网 IP
func getLocalIP() (string, error) {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "", err
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String(), nil
}

// handleScriptHTTPRequest 处理脚本 HTTP 请求
func handleScriptHTTPRequest(w http.ResponseWriter, r *http.Request) {
	// 设置 CORS 头
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// 提取路径中的 identifier
	path := strings.TrimPrefix(r.URL.Path, "/clip-save/")
	if path == "" {
		http.Error(w, "缺少脚本标识符", http.StatusBadRequest)
		return
	}

	// 查找对应的脚本
	enabledScriptsMutex.RLock()
	script, exists := enabledScripts[path]
	enabledScriptsMutex.RUnlock()

	if !exists {
		http.Error(w, "脚本未启用 HTTP 服务", http.StatusNotFound)
		return
	}

	// 提取 content 参数
	var content string
	if r.Method == "GET" {
		content = r.URL.Query().Get("content")
	} else if r.Method == "POST" {
		contentType := r.Header.Get("Content-Type")
		if strings.Contains(contentType, "application/json") {
			// JSON 格式
			var jsonData map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&jsonData); err != nil {
				http.Error(w, fmt.Sprintf("解析 JSON 失败: %v", err), http.StatusBadRequest)
				return
			}
			if val, ok := jsonData["content"].(string); ok {
				content = val
			}
		} else {
			// 表单格式
			if err := r.ParseForm(); err != nil {
				http.Error(w, fmt.Sprintf("解析表单失败: %v", err), http.StatusBadRequest)
				return
			}
			content = r.FormValue("content")
		}
	} else {
		http.Error(w, "不支持的 HTTP 方法", http.StatusMethodNotAllowed)
		return
	}

	// 生成请求 ID
	requestCounterMutex.Lock()
	requestCounter++
	requestID := fmt.Sprintf("req_%d_%d", time.Now().Unix(), requestCounter)
	requestCounterMutex.Unlock()

	// 创建结果通道
	resultChan := make(chan ScriptHTTPResult, 1)
	scriptResultsMutex.Lock()
	scriptResults[requestID] = resultChan
	scriptResultsMutex.Unlock()

	// 通过事件触发脚本执行
	if globalScriptEventCallback != nil {
		globalScriptEventCallback("script.http.execute", map[string]interface{}{
			"requestID": requestID,
			"scriptID":  script.ID,
			"content":   content,
		})
	} else {
		http.Error(w, "脚本执行器未初始化", http.StatusInternalServerError)
		return
	}

	// 等待脚本执行结果（超时 30 秒）
	select {
	case result := <-resultChan:
		// 清理结果通道
		scriptResultsMutex.Lock()
		delete(scriptResults, requestID)
		scriptResultsMutex.Unlock()

		// 返回结果
		w.Header().Set("Content-Type", "application/json")
		if result.Error != "" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": result.Error,
			})
		} else {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"returnValue": result.ReturnValue,
			})
		}
	case <-time.After(30 * time.Second):
		// 超时
		scriptResultsMutex.Lock()
		delete(scriptResults, requestID)
		scriptResultsMutex.Unlock()

		http.Error(w, "脚本执行超时", http.StatusRequestTimeout)
	}
}

// SetScriptHTTPResult 设置脚本执行结果（由前端调用）
func SetScriptHTTPResult(requestID string, result ScriptHTTPResult) {
	scriptResultsMutex.RLock()
	resultChan, exists := scriptResults[requestID]
	scriptResultsMutex.RUnlock()

	if exists {
		select {
		case resultChan <- result:
		default:
			// 通道已满，忽略
		}
	}
}
