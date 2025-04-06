package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/eiannone/keyboard"     // 添加键盘监听库
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
	currentState    State             = Idle // 当前状态，初始为空闲
	conn            *websocket.Conn          // WebSocket连接对象
	enc             *opus.Encoder            // Opus编码器
	dec             *opus.Decoder            // Opus解码器
	stream          *portaudio.Stream        // 音频流
	audioOut        AudioBuffer              // 输出音频缓冲区
	isRecording     bool                     // 是否正在录音
	listeningChan   chan bool                // 用于控制监听状态
	listeningTimer  *time.Timer              // 监听超时计时器
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	err := portaudio.Initialize()
	if err != nil {
		log.Fatalf("初始化音频失败: %v", err)
	}
	defer portaudio.Terminate()

	enc, err = opus.NewEncoder(sampleRate, channels, opus.AppVoIP)
	if err != nil {
		log.Fatalf("初始化 Opus 编码器失败: %v", err)
	}
	dec, err = opus.NewDecoder(sampleRate, channels)
	if err != nil {
		log.Fatalf("初始化 Opus 解码器失败: %v", err)
	}

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	conn, err = connectWebSocket()
	if err != nil {
		log.Fatalf("连接失败: %v", err)
	}
	defer conn.Close()

	err = initAudioStream()
	if err != nil {
		log.Fatalf("初始化音频流失败: %v", err)
	}
	defer stream.Close()

	sendHello()

	done := make(chan struct{})
	listeningChan = make(chan bool)
	go receiveMessages(done)
	go handleKeyboardInput()
	go autoListenController()

	select {
	case <-done:
		log.Println("程序结束")
	case <-interrupt:
		log.Println("收到中断信号，关闭连接...")
		closeAudioChannel()
		time.Sleep(time.Second)
	}
}

func connectWebSocket() (*websocket.Conn, error) {
	log.Printf("状态: %s", Connecting)
	currentState = Connecting

	header := map[string][]string{
		"Authorization":    {authToken},
		"Protocol-Version": {"1"},
		"Device-Id":        {deviceID},
		"Client-Id":        {clientID},
	}

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
	err := conn.WriteJSON(hello)
	if err != nil {
		log.Printf("发送 hello 消息失败: %v", err)
		return
	}
	log.Printf("发送: %v", hello)
}

func initAudioStream() error {
	var err error
	stream, err = portaudio.OpenDefaultStream(channels, channels, float64(sampleRate), sampleRate*frameDurationMs/1000, audioCallback)
	if err != nil {
		return err
	}
	return stream.Start()
}

func audioCallback(in, out []int16) {
	if currentState == Listening {
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

	audioOut.Lock()
	if len(audioOut.data) > 0 && currentState == Speaking {
		copy(out, audioOut.data[0])
		audioOut.data = audioOut.data[1:]
	} else {
		for i := range out {
			out[i] = 0
		}
	}
	audioOut.Unlock()
}

func receiveMessages(done chan<- struct{}) {
	defer close(done)
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
			var msg Message
			if err := json.Unmarshal(data, &msg); err != nil {
				log.Printf("解析 JSON 失败: %v, 数据: %s", err, data)
				continue
			}
			log.Printf("接收: %v", msg)
			handleServerMessage(msg)

		case websocket.BinaryMessage:
			pcm := make([]int16, sampleRate*frameDurationMs/1000)
			_, err := dec.Decode(data, pcm)
			if err != nil {
				log.Printf("Opus 解码失败: %v", err)
				continue
			}
			if currentState == Speaking {
				audioOut.Lock()
				audioOut.data = append(audioOut.data, pcm)
				audioOut.Unlock()
				log.Printf("收到音频数据，长度: %d 样本", len(pcm))
			}

		case websocket.CloseMessage:
			log.Println("收到关闭消息")
			return
		}
	}
}

func handleServerMessage(msg Message) {
	switch msg.Type {
	case "hello":
		if msg.Transport == "websocket" {
			log.Println("服务器握手成功")
			currentState = Connected
			log.Printf("状态: %s", currentState)
		}
	case "stt":
		log.Printf("语音识别结果: %s", msg.Text)
	case "tts":
		switch msg.State {
		case "start":
			currentState = Speaking
			log.Printf("状态: %s", currentState)
			log.Println("开始播放TTS音频")
			audioOut.Lock()
			audioOut.data = nil
			audioOut.Unlock()
		case "stop":
			currentState = Connected
			log.Printf("状态: %s", currentState)
			log.Println("TTS播放结束")
			// AI回复结束后自动开始监听
			go func() {
				time.Sleep(500 * time.Millisecond) // 短暂延迟确保状态切换完成
				listeningChan <- true
			}()
		case "sentence_start":
			log.Printf("TTS句子: %s", msg.Text)
		}
	case "iot":
		log.Printf("收到IoT命令: %v", msg.Commands)
		for _, cmd := range msg.Commands {
			log.Printf("执行 IoT 命令: %s", cmd)
		}
	case "llm":
		log.Printf("LLM 情感: %s, 文本: %s", msg.Emotion, msg.Text)
	default:
		log.Printf("未知消息类型: %s", msg.Type)
	}
}

// 新增：处理键盘输入
func handleKeyboardInput() {
	if err := keyboard.Open(); err != nil {
		log.Printf("无法打开键盘监听: %v", err)
		return
	}
	defer keyboard.Close()

	for {
		char, key, err := keyboard.GetKey()
		if err != nil {
			log.Printf("键盘监听错误: %v", err)
			return
		}
		if key == keyboard.KeySpace {
			if !isRecording {
				startListening()
				isRecording = true
				// 设置10秒超时
				listeningTimer = time.AfterFunc(10*time.Second, func() {
					if isRecording {
						stopListening()
						isRecording = false
						log.Println("10秒超时，自动停止监听")
					}
				})
			} else {
				stopListening()
				isRecording = false
				if listeningTimer != nil {
					listeningTimer.Stop()
				}
			}
		} else if char == '1' || char == '2' || char == '3' || char == '4' || char == '5' || char == '6' {
			// 保留原有数字命令功能
			switch string(char) {
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
			}
		}
	}
}

// 新增：自动监听控制器
func autoListenController() {
	for {
		<-listeningChan
		if currentState == Connected {
			startListening()
			isRecording = true
			// 设置7秒超时
			listeningTimer = time.AfterFunc(7*time.Second, func() {
				if isRecording {
					stopListening()
					isRecording = false
					log.Println("7秒超时，自动停止监听")
				}
			})
		}
	}
}

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

func sendIoTStates() {
	iot := Message{
		SessionID: sessionID,
		Type:      "iot",
		States: map[string]interface{}{
			"temperature": 25.5,
			"light":       "on",
		},
	}
	err := conn.WriteJSON(iot)
	if err != nil {
		log.Printf("发送 IoT 状态失败: %v", err)
		return
	}
	log.Printf("发送: %v", iot)
}

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
