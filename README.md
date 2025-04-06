# XiaoZhi-Go

这是一个基于 `Go` 语言开发的小智([xiaozhi-esp32](https://github.com/78/xiaozhi-esp32))对话程序，通过 `WebSocket` 协议与服务器交互，支持语音输入输出、状态管理和 `IoT` 控制。该程序遵循 [xiaozhi-esp32](https://github.com/78/xiaozhi-esp32/blob/main/docs/websocket.md) 的通信协议，能够连接到 `wss://api.tenclass.net/xiaozhi/v1/` 服务器，实现语音识别 (`STT`)、文本转语音 (`TTS`) 和设备控制功能。

## 项目概述

本程序实现人工智能对话的交互逻辑，主要功能包括：

- **语音输入**: 从麦克风采集音频，编码为 `Opus` 格式并发送到服务器。
- **语音输出**: 接收服务器的 `TTS` 音频，解码并通过扬声器播放。
- **消息交互**: 处理 `JSON` 格式的控制消息，如 `hello`、`listen`、`iot` 等。
- **状态管理**: 实现协议中的状态流转（`Idle` → `Connecting` → `Connected` → `Listening` → `Speaking`）。
- **用户控制**: 通过终端命令控制程序行为。

项目适用于语音助手、智能家居设备或其他需要实时语音交互的场景。

## 功能特性

1. **音频处理**:

   - 实时采集麦克风输入，编码为 `Opus` 格式。
   - 接收并播放服务器返回的 `Opus` 音频。

2. **消息支持**:

   - **客户端发送**: `hello`, `listen` (start/stop/detect), `abort`, `iot` (states/descriptors)。
   - **服务器接收**: `hello`, `stt`, `tts` (start/stop/sentence_start), `iot` (commands), `llm`。

3. **用户交互**:

   - 提供终端命令菜单，支持开始/停止监听、发送唤醒词、中止会话等操作。

4. **状态管理**:

   - 严格遵循协议状态机，确保与服务器同步。

5. **错误处理**:

   - 处理连接断开、音频编解码错误等情况，自动恢复到空闲状态。

## 安装

### 前置条件

- **操作系统**: 支持 `Linux`、`macOS` 或 `Windows`（需安装音频驱动）。
- **硬件**: 麦克风和扬声器。
- **Go 版本**: 1.16 或以上。

### 依赖安装

1. 安装 Go（参考 [官方安装指南](https://golang.org/doc/install)）。
2. 安装系统依赖：
   - **Linux**: `sudo apt-get install libopus-dev portaudio19-dev`
   - **macOS**: `brew install opus portaudio`
   - **Windows**: 使用包管理器（如 MSYS2）安装 `libopus` 和 `portaudio`。
3. 获取代码

```bash
git clone https://github.com/wwwAngHua/xiaozhi-go.git
cd xiaoxhi-go
```

3. 获取 Go 依赖：

```bash
go get github.com/gorilla/websocket
go get github.com/gordonklaus/portaudio
go get github.com/hraban/opus
```

## 使用方法

### 编译与运行

```bash
go run main.go
```

或编译为可执行文件：

```bash
go build -o xiaozhi-go
./xiaozhi-go
```

### 程序交互

程序启动后，会自动连接到服务器并发送 `hello` 消息。终端显示命令菜单：

```text
命令: [1] 开始监听, [2] 停止监听, [3] 发送唤醒词, [4] 中止会话, [5] 发送IoT状态, [6] 退出
```

- 1: 开始监听，采集麦克风音频并发送到服务器。
- 2: 停止监听，结束音频发送。
- 3: 发送唤醒词（如“你好小智”），触发服务器响应。
- 4: 中止当前会话，停止播放或监听。
- 5: 发送示例 IoT 状态（如温度和灯光）。
- 6: 退出程序，关闭连接。

✨ <strong style="color: green;">[NEW] 除了以上交互方式外，目前新增了一个更方便的交互方式，程序启动连接成功后可以按一下空格开始说话，说话完毕再次按下空格可以得到回复，回复完毕后自动进入监听状态，只需要说话即可，再次按下即可结束，输入数字 6 可以退出程序</strong>

### 示例运行日志

```text
2025/04/05 10:00:00 main.go:105: 状态: Connecting
2025/04/05 10:00:01 main.go:122: 状态: Connected
2025/04/05 10:00:01 main.go:123: WebSocket 连接成功
2025/04/05 10:00:01 main.go:134: 发送: {Type:hello Version:1 Transport:websocket AudioParams:{Format:opus SampleRate:16000 Channels:1 FrameDuration:60}}

命令: [1] 开始监听, [2] 停止监听, [3] 发送唤醒词, [4] 中止会话, [5] 发送IoT状态, [6] 退出
1
2025/04/05 10:00:05 main.go:254: 状态: Listening
2025/04/05 10:00:05 main.go:256: 发送: {Type:listen SessionID:session_123 State:start Mode:manual}
2025/04/05 10:00:06 main.go:xxx: 接收: {Type:stt Text:你好}
2025/04/05 10:00:07 main.go:xxx: 接收: {Type:tts State:start}
2025/04/05 10:00:07 main.go:xxx: 状态: Speaking
2025/04/05 10:00:07 main.go:xxx: 收到音频数据，长度: 960 样本
```

## 配置说明

### 默认配置

- 服务器地址: `wss://api.tenclass.net/xiaozhi/v1/`
- 认证令牌: `Bearer test-token`
- 设备 ID: `b5:4a:56:ad:ef:f9`
- 音频参数: 采样率 `16000 Hz`，单声道，帧时长 `60ms`。
- 自定义配置
- 在 `main.go` 中修改以下常量：

```go
const (
    wsURL      = "wss://your-server-url" // 替换为您的服务器地址
    authToken  = "Bearer your-token"     // 替换为有效令牌
    deviceID   = "your-device-id"        // 替换为设备MAC地址
    clientID   = "your-client-id"        // 替换为客户端ID
    sessionID  = "your-session-id"       // 替换为会话ID
)
```

一般情况下，只需要修改 `deviceID` 为您的 `ESP32 MAC` 地址即可。

## 注意事项

1. 服务器兼容性:
   - 确保服务器支持协议中的消息格式和 `Opus` 音频编码。
   - 若 `test-token` 无效，请联系服务器管理员获取有效令牌。
2. 音频设备:
   - 运行前检查麦克风和扬声器是否可用，否则程序会报错。
   - 可通过 `portaudio` 的调试工具检查设备：

```go
go run -tags portaudio main.go
```

3. 性能优化:

- 当前音频缓冲使用简单队列，高负载下可能出现延迟，可优化为环形缓冲区。

4. 安全性:

- 默认令牌硬编码在代码中，生产环境应使用环境变量或配置文件管理。

## 贡献代码

欢迎提交 Pull Request 或 Issue！以下是贡献步骤：

1. Fork 本仓库。
2. 创建分支：`git checkout -b feature/your-feature`。
3. 提交更改：`git commit -m "添加新功能"`。
4. 推送分支：`git push origin feature/your-feature`。
5. 创建 `Pull Request`。

## 开发建议

- UI 改进: 添加图形界面（如使用 `fyne`）。
- 功能扩展: 支持更多 `IoT` 命令或自定义消息类型。
- 错误恢复: 实现断线重连机制。

## 许可证

本项目采用 `MIT` 协议。详情见 `LICENSE` 文件。

## 联系方式

作者: wwwAngHua
QQ: 422584084
微信: kingstudy-vip
邮箱: wwwanghua@outlook.com
Issues: [GitHub Issues](https://github.com/wwwAngHua/xiaozhi-go/issues)

## 特别感谢

最后感谢虾哥 [Xiaoxia](https://github.com/78) 提供的 [xiaozhi-esp32](https://github.com/78/xiaozhi-esp32) 项目模型服务器的支持，大公无私！
