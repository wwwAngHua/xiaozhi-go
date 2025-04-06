package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/gordonklaus/portaudio" // 音频输入输出库
	"github.com/gorilla/websocket"     // WebSocket 通信库
	"github.com/hraban/opus"           // Opus 音频编解码库
)

const (
	// WebSocket 配置常量
	wsURL           = "wss://api.tenclass.net/xiaozhi/v1/" // WebSocket 服务器地址
	authToken       = "Bearer test-token"                  // 认证令牌
	deviceID        = "b5:4a:56:ad:ef:f9"                  // 设备ID（MAC地址）
	clientID        = "client_123"                         // 客户端ID
	sessionID       = "session_123"                        // 会话ID
	sampleRate      = 16000                                // 音频采样率（Hz）
	channels        = 1                                    // 音频通道数（单声道）
	frameDurationMs = 60                                   // 每帧时长（毫秒）
)

// State 定义设备状态
type State string

const (
	Idle       State = "Idle"       // 空闲状态
	Connecting State = "Connecting" // 连接中状态
	Connected  State = "Connected"  // 已连接状态
	Listening  State = "Listening"  // 监听状态
	Speaking   State = "Speaking"   // 播放状态
)

// AudioParams 定义音频参数结构
type AudioParams struct {
	Format        string `json:"format"`         // 音频格式（如 "opus"）
	SampleRate    int    `json:"sample_rate"`    // 采样率
	Channels      int    `json:"channels"`       // 通道数
	FrameDuration int    `json:"frame_duration"` // 帧时长（ms）
}

// Message 定义 WebSocket 消息结构
type Message struct {
	Type        string      `json:"type"`                   // 消息类型
	Version     int         `json:"version,omitempty"`      // 协议版本
	Transport   string      `json:"transport,omitempty"`    // 传输方式
	AudioParams AudioParams `json:"audio_params,omitempty"` // 音频参数
	SessionID   string      `json:"session_id,omitempty"`   // 会话ID
	State       string      `json:"state,omitempty"`        // 状态（如 start/stop）
	Mode        string      `json:"mode,omitempty"`         // 模式（如 manual/auto）
	Text        string      `json:"text,omitempty"`         // 文本内容
	Reason      string      `json:"reason,omitempty"`       // 原因（如中止原因）
	Descriptors interface{} `json:"descriptors,omitempty"`  // IoT描述信息
	States      interface{} `json:"states,omitempty"`       // IoT状态信息
	Commands    []string    `json:"commands,omitempty"`     // IoT命令
	Emotion     string      `json:"emotion,omitempty"`      // LLM情感
}

// AudioBuffer 用于缓冲音频输出数据
type AudioBuffer struct {
	sync.Mutex           // 互斥锁，确保线程安全
	data       [][]int16 // PCM音频数据缓冲区
}

var (
	currentState State             = Idle // 当前状态，初始为空闲
	conn         *websocket.Conn          // WebSocket连接对象
	enc          *opus.Encoder            // Opus编码器
	dec          *opus.Decoder            // Opus解码器
	stream       *portaudio.Stream        // 音频流
	audioOut     AudioBuffer              // 输出音频缓冲区
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile) // 设置日志格式，包含时间和文件名

	// 初始化音频库
	err := portaudio.Initialize()
	if err != nil {
		log.Fatalf("初始化音频失败: %v", err)
	}
	defer portaudio.Terminate() // 程序结束时释放音频资源

	// 初始化Opus编码器，用于将PCM音频编码为Opus格式
	enc, err = opus.NewEncoder(sampleRate, channels, opus.AppVoIP)
	if err != nil {
		log.Fatalf("初始化 Opus 编码器失败: %v", err)
	}
	// 初始化Opus解码器，用于解码服务器发送的Opus音频
	dec, err = opus.NewDecoder(sampleRate, channels)
	if err != nil {
		log.Fatalf("初始化 Opus 解码器失败: %v", err)
	}

	// 设置中断信号处理，捕获Ctrl+C等退出信号
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	// 建立WebSocket连接
	conn, err = connectWebSocket()
	if err != nil {
		log.Fatalf("连接失败: %v", err)
	}
	defer conn.Close() // 程序结束时关闭连接

	// 初始化音频流，用于麦克风输入和扬声器输出
	err = initAudioStream()
	if err != nil {
		log.Fatalf("初始化音频流失败: %v", err)
	}
	defer stream.Close() // 程序结束时关闭音频流

	// 发送hello消息进行握手
	sendHello()

	// 主循环，处理消息和用户交互
	done := make(chan struct{})
	go receiveMessages(done) // 启动消息接收协程
	go userInteraction()     // 启动用户交互协程

	select {
	case <-done: // 消息接收协程结束
		log.Println("程序结束")
	case <-interrupt: // 收到中断信号
		log.Println("收到中断信号，关闭连接...")
		closeAudioChannel()
		time.Sleep(time.Second) // 等待关闭完成
	}
}

// connectWebSocket 建立WebSocket连接
func connectWebSocket() (*websocket.Conn, error) {
	log.Printf("状态: %s", Connecting)
	currentState = Connecting

	// 设置请求头
	header := map[string][]string{
		"Authorization":    {authToken}, // 认证令牌
		"Protocol-Version": {"1"},       // 协议版本
		"Device-Id":        {deviceID},  // 设备ID
		"Client-Id":        {clientID},  // 客户端ID
	}

	// 尝试连接服务器
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		currentState = Idle
		return nil, fmt.Errorf("WebSocket 连接失败: %v", err)
	}

	log.Printf("状态: %s", Connected)
	currentState = Connected
	log.Println("WebSocket 连接成功")
	return conn, nil
}

// sendHello 发送hello消息进行握手
func sendHello() {
	hello := Message{
		Type:      "hello",
		Version:   1,
		Transport: "websocket",
		AudioParams: AudioParams{
			Format:        "opus",
			SampleRate:    sampleRate,
			Channels:      channels,
			FrameDuration: frameDurationMs,
		},
	}
	err := conn.WriteJSON(hello) // 将消息序列化为JSON并发送
	if err != nil {
		log.Printf("发送 hello 消息失败: %v", err)
		return
	}
	log.Printf("发送: %v", hello)
}

// initAudioStream 初始化音频输入输出流
func initAudioStream() error {
	var err error
	// 创建默认音频流，指定输入输出通道数、采样率和每帧样本数
	stream, err = portaudio.OpenDefaultStream(channels, channels, float64(sampleRate), sampleRate*frameDurationMs/1000, audioCallback)
	if err != nil {
		return err
	}
	return stream.Start() // 启动音频流
}

// audioCallback 音频处理回调函数
func audioCallback(in, out []int16) {
	if currentState == Listening {
		// 在监听状态下，将麦克风输入编码为Opus并发送
		data := make([]byte, 1024)
		n, err := enc.Encode(in, data)
		if err != nil {
			log.Printf("Opus 编码失败: %v", err)
			return
		}
		err = conn.WriteMessage(websocket.BinaryMessage, data[:n])
		if err != nil {
			log.Printf("发送音频数据失败: %v", err)
		}
	}

	// 处理音频输出
	audioOut.Lock()
	if len(audioOut.data) > 0 && currentState == Speaking {
		// 如果有缓冲的音频数据且在播放状态，输出到扬声器
		copy(out, audioOut.data[0])
		audioOut.data = audioOut.data[1:]
	} else {
		// 没有音频数据时输出静音
		for i := range out {
			out[i] = 0
		}
	}
	audioOut.Unlock()
}

// receiveMessages 接收服务器消息
func receiveMessages(done chan<- struct{}) {
	defer close(done) // 协程结束时关闭done通道
	for {
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			log.Printf("读取消息失败: %v", err)
			currentState = Idle
			log.Printf("状态: %s", currentState)
			return
		}

		switch msgType {
		case websocket.TextMessage:
			// 处理文本消息（JSON格式）
			var msg Message
			if err := json.Unmarshal(data, &msg); err != nil {
				log.Printf("解析 JSON 失败: %v, 数据: %s", err, data)
				continue
			}
			log.Printf("接收: %v", msg)
			handleServerMessage(msg)

		case websocket.BinaryMessage:
			// 处理二进制消息（Opus音频）
			pcm := make([]int16, sampleRate*frameDurationMs/1000)
			_, err := dec.Decode(data, pcm)
			if err != nil {
				log.Printf("Opus 解码失败: %v", err)
				continue
			}
			if currentState == Speaking {
				audioOut.Lock()
				audioOut.data = append(audioOut.data, pcm) // 将解码后的PCM数据加入缓冲区
				audioOut.Unlock()
				log.Printf("收到音频数据，长度: %d 样本", len(pcm))
			}

		case websocket.CloseMessage:
			log.Println("收到关闭消息")
			return
		}
	}
}

// handleServerMessage 处理服务器发送的消息
func handleServerMessage(msg Message) {
	switch msg.Type {
	case "hello":
		if msg.Transport == "websocket" {
			log.Println("服务器握手成功")
			currentState = Connected
			log.Printf("状态: %s", currentState)
		}
	case "stt":
		log.Printf("语音识别结果: %s", msg.Text) // 显示语音转文本结果
	case "tts":
		switch msg.State {
		case "start":
			currentState = Speaking
			log.Printf("状态: %s", currentState)
			log.Println("开始播放TTS音频")
			audioOut.Lock()
			audioOut.data = nil // 清空音频缓冲区
			audioOut.Unlock()
		case "stop":
			currentState = Connected
			log.Printf("状态: %s", currentState)
			log.Println("TTS播放结束")
		case "sentence_start":
			log.Printf("TTS句子: %s", msg.Text) // 显示当前播放的句子
		}
	case "iot":
		log.Printf("收到IoT命令: %v", msg.Commands)
		// 模拟执行IoT命令
		for _, cmd := range msg.Commands {
			log.Printf("执行 IoT 命令: %s", cmd)
		}
	case "llm":
		log.Printf("LLM 情感: %s, 文本: %s", msg.Emotion, msg.Text) // 显示LLM情感和文本
	default:
		log.Printf("未知消息类型: %s", msg.Type)
	}
}

// userInteraction 处理用户交互
func userInteraction() {
	for {
		// 显示命令菜单
		fmt.Println("\n命令: [1] 开始监听, [2] 停止监听, [3] 发送唤醒词, [4] 中止会话, [5] 发送IoT状态, [6] 退出")
		var input string
		fmt.Scanln(&input)

		switch input {
		case "1":
			startListening()
		case "2":
			stopListening()
		case "3":
			sendWakeWord("你好小智")
		case "4":
			abortSession("user_request")
		case "5":
			sendIoTStates()
		case "6":
			closeAudioChannel()
			return
		default:
			fmt.Println("无效命令")
		}
	}
}

// startListening 开始监听
func startListening() {
	if currentState != Connected {
		log.Println("请先建立连接")
		return
	}
	listen := Message{
		SessionID: sessionID,
		Type:      "listen",
		State:     "start",
		Mode:      "manual",
	}
	err := conn.WriteJSON(listen)
	if err != nil {
		log.Printf("发送 listen 消息失败: %v", err)
		return
	}
	currentState = Listening
	log.Printf("状态: %s", currentState)
	log.Printf("发送: %v", listen)
}

// stopListening 停止监听
func stopListening() {
	if currentState != Listening {
		log.Println("当前未在监听状态")
		return
	}
	listen := Message{
		SessionID: sessionID,
		Type:      "listen",
		State:     "stop",
		Mode:      "manual",
	}
	err := conn.WriteJSON(listen)
	if err != nil {
		log.Printf("发送 stop 消息失败: %v", err)
		return
	}
	currentState = Connected
	log.Printf("状态: %s", currentState)
	log.Printf("发送: %v", listen)
}

// sendWakeWord 发送唤醒词
func sendWakeWord(text string) {
	if currentState != Listening {
		log.Println("请先开始监听")
		return
	}
	wake := Message{
		SessionID: sessionID,
		Type:      "listen",
		State:     "detect",
		Text:      text,
	}
	err := conn.WriteJSON(wake)
	if err != nil {
		log.Printf("发送 wake word 消息失败: %v", err)
		return
	}
	log.Printf("发送: %v", wake)
}

// abortSession 中止会话
func abortSession(reason string) {
	abort := Message{
		SessionID: sessionID,
		Type:      "abort",
		Reason:    reason,
	}
	err := conn.WriteJSON(abort)
	if err != nil {
		log.Printf("发送 abort 消息失败: %v", err)
		return
	}
	currentState = Connected
	log.Printf("状态: %s", currentState)
	log.Printf("发送: %v", abort)
}

// sendIoTStates 发送IoT状态
func sendIoTStates() {
	iot := Message{
		SessionID: sessionID,
		Type:      "iot",
		States: map[string]interface{}{
			"temperature": 25.5, // 示例温度状态
			"light":       "on", // 示例灯光状态
		},
	}
	err := conn.WriteJSON(iot)
	if err != nil {
		log.Printf("发送 IoT 状态失败: %v", err)
		return
	}
	log.Printf("发送: %v", iot)
}

// closeAudioChannel 关闭音频通道
func closeAudioChannel() {
	if conn != nil {
		err := conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		if err != nil {
			log.Printf("关闭连接失败: %v", err)
		}
		currentState = Idle
		log.Printf("状态: %s", currentState)
	}
}
