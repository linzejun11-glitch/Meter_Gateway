// Package mqttclient 负责电表网关与Python MQTT Broker之间的通信。
package mqttclient

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"MOCK_COLLECT/common/config"
	"MOCK_COLLECT/common/protocol"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

const (
	mqttProtocolVersion = 4 // Paho用4表示MQTT 3.1.1。
	mqttQoS             = 0
	publishWaitTimeout  = 5 * time.Second
	interFrameDelay     = 50 * time.Millisecond
	internalTimeLayout  = "2006-01-02 15:04:05"
	serverTimeLayout    = "20060102150405"
)

// Client 只保存电表上报和DO控制所需的MQTT状态。
type Client struct {
	cfg     config.MQTTConfig
	meterID byte

	mqttClient mqtt.Client
	commands   chan protocol.SwitchControl
	ready      chan struct{}
	readyOnce  sync.Once

	startupErrors    chan error
	startupErrorOnce sync.Once

	// pendingMu保护编号生成器和两张等待回复表，因为采集和控制可能并发发送。
	pendingMu            sync.Mutex
	nextSessionID        uint16
	nextBusinessID       int64
	nextControlSessionID uint16
	pendingBySession     map[uint16]*pendingPost
	pendingByBusiness    map[string]*pendingPost

	stopped  chan struct{}
	stopOnce sync.Once
}

// postMessage 是服务端要求的属性增量上报JSON。
type postMessage struct {
	ID     string         `json:"id"`
	Params map[string]any `json:"params"`
}

// postReply 是服务端对属性上报的处理结果。
type postReply struct {
	ID        string  `json:"id,omitempty"`
	SessionID *uint16 `json:"session_id,omitempty"`
	Cmd       int     `json:"cmd"`
	Loss      []int   `json:"loss,omitempty"`
}

// pendingPost 把业务id和分片session_id关联到同一次上报。
type pendingPost struct {
	businessID string
	sessionID  uint16
	replies    chan postReply
}

// setRequest兼容服务端用布尔、数字或字符串表示DO状态。
type setRequest struct {
	ID     string         `json:"id"`
	Params map[string]any `json:"params"`
}

type setReply struct {
	ID   string `json:"id"`
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

// NewClient创建对象；真正连接网络要调用Run。
func NewClient(cfg config.MQTTConfig, meterID byte) *Client {
	client := &Client{
		cfg:                  cfg,
		meterID:              meterID,
		commands:             make(chan protocol.SwitchControl, 20),
		ready:                make(chan struct{}),
		startupErrors:        make(chan error, 1),
		nextSessionID:        randomSessionID(),
		nextBusinessID:       time.Now().UnixMilli() - 1,
		nextControlSessionID: randomSessionID(),
		pendingBySession:     make(map[uint16]*pendingPost),
		pendingByBusiness:    make(map[string]*pendingPost),
		stopped:              make(chan struct{}),
	}

	options := mqtt.NewClientOptions()
	options.AddBroker(cfg.Broker)
	// Python Broker约定设备名同时用作Client ID和Username。
	options.SetClientID(cfg.DeviceName)
	options.SetUsername(cfg.DeviceName)
	options.SetPassword(cfg.Password)
	options.SetProtocolVersion(mqttProtocolVersion)
	options.SetCleanSession(true)
	options.SetKeepAlive(60 * time.Second)
	options.SetPingTimeout(10 * time.Second)
	options.SetConnectTimeout(10 * time.Second)
	options.SetAutoReconnect(true)
	options.SetConnectRetry(false)

	// 首次连接及自动重连成功后都重新订阅电表所需Topic。
	options.SetOnConnectHandler(func(_ mqtt.Client) {
		log.Printf("Python MQTT Broker连接成功")
		if err := client.subscribe(); err != nil {
			log.Printf("订阅Python MQTT Topic失败：%v", err)
			client.reportStartupError(err)
			return
		}
		client.readyOnce.Do(func() { close(client.ready) })
	})
	options.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		log.Printf("Python MQTT连接断开：%v", err)
	})

	client.mqttClient = mqtt.NewClient(options)
	return client
}

func randomSessionID() uint16 {
	var data [2]byte
	if _, err := rand.Read(data[:]); err == nil {
		return binary.BigEndian.Uint16(data[:])
	}
	return 0
}

// Run连接Broker并保持运行，直到ctx收到退出通知。
func (c *Client) Run(ctx context.Context) error {
	if c.cfg.Broker == "" {
		return errors.New("Python MQTT Broker地址为空")
	}
	if c.cfg.DeviceName == "" {
		return errors.New("MQTT设备名称为空")
	}
	if c.cfg.Password == "" {
		return errors.New("MQTT设备密码为空")
	}

	log.Printf("正在连接Python MQTT Broker：broker=%s device_name=%s",
		c.cfg.Broker, c.cfg.DeviceName)
	token := c.mqttClient.Connect()
	if !token.WaitTimeout(12 * time.Second) {
		c.disconnect()
		return errors.New("连接Python MQTT Broker超时")
	}
	if err := token.Error(); err != nil {
		c.disconnect()
		return fmt.Errorf("连接Python MQTT Broker失败: %w", err)
	}

	select {
	case <-c.ready:
		log.Printf("Python MQTT登录和Topic订阅完成，电表网关已就绪")
	case err := <-c.startupErrors:
		c.disconnect()
		return fmt.Errorf("Python MQTT客户端启动失败: %w", err)
	case <-ctx.Done():
		c.stop()
		c.disconnect()
		return nil
	}

	<-ctx.Done()
	c.stop()
	c.disconnect()
	log.Printf("Python MQTT连接已关闭")
	return nil
}

func (c *Client) Ready() <-chan struct{} { return c.ready }

// Commands向电表控制器提供服务器下发的DO命令。
func (c *Client) Commands() <-chan protocol.SwitchControl { return c.commands }

// Publish只接受电表测量、开关状态和电表控制ACK。
func (c *Client) Publish(message any) error {
	if !c.mqttClient.IsConnectionOpen() {
		return errors.New("Python MQTT连接尚未建立")
	}
	switch value := message.(type) {
	case protocol.ControlACK:
		return c.publishControlACK(value)
	case protocol.MeterData, protocol.SwitchStatus:
		return c.publishPost(value)
	default:
		return fmt.Errorf("Python MQTT不支持的电表消息类型：%T", message)
	}
}

// publishPost负责JSON转换、分帧、发布、确认和重传。
func (c *Client) publishPost(message any) error {
	sessionID, businessID, replies, err := c.registerPending()
	if err != nil {
		return err
	}
	defer c.removePending(sessionID, businessID)

	payload, err := marshalPostMessage(businessID, message)
	if err != nil {
		return err
	}
	frames, err := buildFrames(sessionID, payload, c.cfg.ChunkSize)
	if err != nil {
		return err
	}
	if err := c.publishFrames(frames, allFrameIndexes(frames)); err != nil {
		return err
	}

	retransmits := 0
	for {
		reply, err := c.waitPostReply(sessionID, replies)
		if err != nil {
			return err
		}
		switch reply.Cmd {
		case 0:
			log.Printf("MQTT会话%d上报成功：共%d帧，重传%d轮",
				sessionID, len(frames), retransmits)
			return nil
		case 1:
			return fmt.Errorf("MQTT会话%d被拒绝：JSON或数据格式错误（cmd=1）", sessionID)
		case 2:
			retransmits++
			if retransmits > c.cfg.MaxRetransmits {
				return fmt.Errorf("MQTT会话%d超过最大重传轮数%d",
					sessionID, c.cfg.MaxRetransmits)
			}
			indexes, lossErr := validateLossIndexes(reply.Loss, len(frames))
			if lossErr != nil {
				return fmt.Errorf("MQTT会话%d补发清单无效: %w", sessionID, lossErr)
			}
			log.Printf("MQTT会话%d第%d轮选择重传：%v",
				sessionID, retransmits, indexes)
			if err := c.publishFrames(frames, indexes); err != nil {
				return err
			}
		case 3:
			retransmits++
			if retransmits > c.cfg.MaxRetransmits {
				return fmt.Errorf("MQTT会话%d超过最大重传轮数%d",
					sessionID, c.cfg.MaxRetransmits)
			}
			log.Printf("MQTT会话%d第%d轮全部重传", sessionID, retransmits)
			if err := c.publishFrames(frames, allFrameIndexes(frames)); err != nil {
				return err
			}
		case 4:
			return fmt.Errorf("MQTT会话%d被拒绝：数据超出允许范围（cmd=4）", sessionID)
		case 5:
			return fmt.Errorf("MQTT会话%d被服务端放弃（cmd=5）", sessionID)
		default:
			return fmt.Errorf("MQTT会话%d收到未知回复cmd=%d", sessionID, reply.Cmd)
		}
	}
}

func (c *Client) publishControlACK(ack protocol.ControlACK) error {
	requestID, payload, err := marshalSetReply(ack)
	if err != nil {
		return err
	}
	// JSON业务id原样回复；帧session_id独立循环生成。
	sessionID := c.nextControlFrameID()
	frame, err := protocol.EncodeMQTTSingleFrame(sessionID, payload)
	if err != nil {
		return fmt.Errorf("生成控制ACK单帧失败: %w", err)
	}
	if err := c.publishMQTT(c.setReplyTopic(), frame); err != nil {
		return fmt.Errorf("发布控制ACK失败: %w", err)
	}
	log.Printf("已回复电表控制结果：id=%s session_id=%d code=%d topic=%s",
		requestID, sessionID, controlReplyCode(ack), c.setReplyTopic())
	return nil
}

func marshalSetReply(ack protocol.ControlACK) (string, []byte, error) {
	if !validBusinessID(ack.RequestID) {
		return "", nil, fmt.Errorf("控制ACK的request_id必须是13位数字字符串：%q",
			ack.RequestID)
	}
	reply := setReply{ID: ack.RequestID, Code: controlReplyCode(ack), Msg: "success"}
	if !ack.Success {
		reply.Msg = ack.Message
		if reply.Msg == "" {
			reply.Msg = ack.ErrorCode
		}
		if reply.Msg == "" {
			reply.Msg = "command failed"
		}
	}
	payload, err := json.Marshal(reply)
	if err != nil {
		return "", nil, fmt.Errorf("生成控制ACK JSON失败: %w", err)
	}
	return ack.RequestID, payload, nil
}

func controlReplyCode(ack protocol.ControlACK) int {
	if ack.Success {
		return 0
	}
	return 1
}

// subscribe只订阅电表上报回复和电表控制两个Topic。
func (c *Client) subscribe() error {
	subscriptions := []struct {
		topic   string
		handler mqtt.MessageHandler
	}{
		{c.postReplyTopic(), c.handlePostReply},
		{c.setTopic(), c.handleSet},
	}
	for _, subscription := range subscriptions {
		token := c.mqttClient.Subscribe(
			subscription.topic, mqttQoS, subscription.handler)
		if !token.WaitTimeout(publishWaitTimeout) {
			return fmt.Errorf("订阅Topic超时：%s", subscription.topic)
		}
		if err := token.Error(); err != nil {
			return fmt.Errorf("订阅Topic失败 %s: %w", subscription.topic, err)
		}
		log.Printf("已订阅Python MQTT Topic：%s", subscription.topic)
	}
	return nil
}

func (c *Client) handlePostReply(_ mqtt.Client, message mqtt.Message) {
	var reply postReply
	if err := json.Unmarshal(message.Payload(), &reply); err != nil {
		log.Printf("解析Python MQTT上报回复失败：%v", err)
		return
	}
	pending, exists := c.findPending(reply)
	if !exists {
		sessionText := "未提供"
		if reply.SessionID != nil {
			sessionText = strconv.Itoa(int(*reply.SessionID))
		}
		log.Printf("收到无法匹配的MQTT回复：id=%q session_id=%s cmd=%d",
			reply.ID, sessionText, reply.Cmd)
		return
	}
	select {
	case pending.replies <- reply:
	default:
		log.Printf("MQTT会话%d（业务id=%s）回复队列已满，忽略cmd=%d",
			pending.sessionID, pending.businessID, reply.Cmd)
	}
}

func (c *Client) handleSet(_ mqtt.Client, message mqtt.Message) {
	command, err := decodeSetRequest(message.Payload(), c.meterID)
	if err != nil {
		log.Printf("丢弃无效电表控制命令：%v", err)
		return
	}
	select {
	case c.commands <- command:
		log.Printf("电表控制命令已交给控制器：id=%s channel=%d state=%t",
			command.RequestID, command.Channel, *command.DesiredState)
	default:
		log.Printf("电表控制命令队列已满，丢弃id=%s", command.RequestID)
	}
}

func decodeSetRequest(payload []byte, meterID byte) (protocol.SwitchControl, error) {
	var request setRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		return protocol.SwitchControl{}, fmt.Errorf("解析set JSON失败: %w", err)
	}
	if !validBusinessID(request.ID) {
		return protocol.SwitchControl{}, fmt.Errorf("set id必须是13位数字字符串：%q",
			request.ID)
	}
	if len(request.Params) != 1 {
		return protocol.SwitchControl{}, fmt.Errorf(
			"每条set命令必须且只能控制一路DO，实际参数数量为%d", len(request.Params))
	}

	channel, name := 0, ""
	var raw any
	if value, exists := request.Params["do1"]; exists {
		channel, name, raw = 1, "do1", value
	} else if value, exists := request.Params["do2"]; exists {
		channel, name, raw = 2, "do2", value
	} else {
		return protocol.SwitchControl{}, errors.New("set命令只支持do1或do2")
	}
	state, err := parseBinarySetState(raw, name)
	if err != nil {
		return protocol.SwitchControl{}, err
	}
	stateCopy := state
	return protocol.SwitchControl{
		MessageType:  protocol.MessageTypeSwitchControl,
		RequestID:    request.ID,
		MeterID:      int(meterID),
		Channel:      channel,
		DesiredState: &stateCopy,
		Timestamp:    protocol.FormatTimestamp(time.Now()),
	}, nil
}

// parseBinarySetState兼容物模型常见的三种开关值写法。
func parseBinarySetState(raw any, name string) (bool, error) {
	switch value := raw.(type) {
	case bool:
		return value, nil
	case string:
		if value == "1" {
			return true, nil
		}
		if value == "0" {
			return false, nil
		}
	case float64: // encoding/json默认把JSON数字解析成float64。
		if value == 1 {
			return true, nil
		}
		if value == 0 {
			return false, nil
		}
	}
	return false, fmt.Errorf(
		"%s只能是true/false、数字0/1或字符串\"0\"/\"1\"，实际为%v",
		name, raw)
}

func (c *Client) publishFrames(frames [][]byte, indexes []int) error {
	for position, index := range indexes {
		if err := c.publishMQTT(c.postTopic(), frames[index]); err != nil {
			return fmt.Errorf("MQTT会话帧%d发布失败: %w", index, err)
		}
		if position < len(indexes)-1 {
			timer := time.NewTimer(interFrameDelay)
			select {
			case <-timer.C:
			case <-c.stopped:
				if !timer.Stop() {
					<-timer.C
				}
				return errors.New("MQTT客户端正在停止")
			}
		}
	}
	return nil
}

func (c *Client) publishMQTT(topic string, payload []byte) error {
	if !c.mqttClient.IsConnectionOpen() {
		return errors.New("Python MQTT连接尚未建立")
	}
	token := c.mqttClient.Publish(topic, mqttQoS, false, payload)
	if !token.WaitTimeout(publishWaitTimeout) {
		return fmt.Errorf("写入Topic超时：%s", topic)
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("发布Topic失败 %s: %w", topic, err)
	}
	return nil
}

func (c *Client) waitPostReply(
	sessionID uint16,
	replies <-chan postReply,
) (postReply, error) {
	timer := time.NewTimer(c.cfg.ReplyTimeout)
	defer timer.Stop()
	select {
	case reply := <-replies:
		return reply, nil
	case <-timer.C:
		return postReply{}, fmt.Errorf("MQTT会话%d等待服务端回复超时（%s）",
			sessionID, c.cfg.ReplyTimeout)
	case <-c.stopped:
		return postReply{}, errors.New("MQTT客户端正在停止")
	}
}

func (c *Client) registerPending() (uint16, string, chan postReply, error) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()

	businessID := c.nextBusinessIDLocked()
	for attempts := 0; attempts <= int(^uint16(0)); attempts++ {
		c.nextSessionID++
		if _, exists := c.pendingBySession[c.nextSessionID]; exists {
			continue
		}
		replies := make(chan postReply, 1)
		pending := &pendingPost{businessID, c.nextSessionID, replies}
		c.pendingBySession[c.nextSessionID] = pending
		c.pendingByBusiness[businessID] = pending
		return c.nextSessionID, businessID, replies, nil
	}
	return 0, "", nil, errors.New("没有可用的MQTT会话ID")
}

func (c *Client) removePending(sessionID uint16, businessID string) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	if pending := c.pendingBySession[sessionID]; pending != nil &&
		pending.businessID == businessID {
		delete(c.pendingBySession, sessionID)
	}
	if pending := c.pendingByBusiness[businessID]; pending != nil &&
		pending.sessionID == sessionID {
		delete(c.pendingByBusiness, businessID)
	}
}

func (c *Client) findPending(reply postReply) (*pendingPost, bool) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()

	if reply.Cmd == 2 || reply.Cmd == 3 || reply.Cmd == 5 {
		if reply.SessionID == nil {
			return nil, false
		}
		pending, exists := c.pendingBySession[*reply.SessionID]
		return pending, exists
	}
	if reply.ID != "" {
		if pending, exists := c.pendingByBusiness[reply.ID]; exists {
			return pending, true
		}
		// 兼容服务端把session_id左侧补零后放入13位id字段的兜底回复。
		if value, err := strconv.ParseUint(reply.ID, 10, 16); err == nil {
			pending, exists := c.pendingBySession[uint16(value)]
			return pending, exists
		}
	}
	if reply.SessionID != nil {
		pending, exists := c.pendingBySession[*reply.SessionID]
		return pending, exists
	}
	return nil, false
}

// nextBusinessIDLocked按Unix毫秒生成13位业务编号；调用者必须持有pendingMu。
func (c *Client) nextBusinessIDLocked() string {
	now := time.Now().UnixMilli()
	if now <= c.nextBusinessID {
		now = c.nextBusinessID + 1
	}
	c.nextBusinessID = now
	return fmt.Sprintf("%013d", now)
}

func (c *Client) nextControlFrameID() uint16 {
	c.pendingMu.Lock()
	c.nextControlSessionID++
	id := c.nextControlSessionID
	c.pendingMu.Unlock()
	return id
}

func (c *Client) postTopic() string {
	return fmt.Sprintf("$sys/%s/post", c.cfg.DeviceName)
}
func (c *Client) postReplyTopic() string {
	return fmt.Sprintf("$sys/%s/post/reply", c.cfg.DeviceName)
}
func (c *Client) setTopic() string {
	return fmt.Sprintf("$sys/%s/set", c.cfg.DeviceName)
}
func (c *Client) setReplyTopic() string {
	return fmt.Sprintf("$sys/%s/set/reply", c.cfg.DeviceName)
}

func (c *Client) reportStartupError(err error) {
	select {
	case <-c.ready:
		return
	default:
	}
	c.startupErrorOnce.Do(func() { c.startupErrors <- err })
}

func (c *Client) stop() {
	c.stopOnce.Do(func() { close(c.stopped) })
}

func (c *Client) disconnect() {
	if c.mqttClient.IsConnectionOpen() {
		c.mqttClient.Disconnect(500)
	}
}

func marshalPostMessage(businessID string, message any) ([]byte, error) {
	if !validBusinessID(businessID) {
		return nil, fmt.Errorf("MQTT业务id必须是13位数字字符串：%q", businessID)
	}
	params, err := postParams(message)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(postMessage{ID: businessID, Params: params})
	if err != nil {
		return nil, fmt.Errorf("生成Python MQTT上报JSON失败: %w", err)
	}
	return payload, nil
}

func validBusinessID(value string) bool {
	if len(value) != 13 {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func postParams(message any) (map[string]any, error) {
	switch value := message.(type) {
	case protocol.MeterData:
		return map[string]any{
			"meter_id": value.MeterID, "voltage": value.Voltage,
			"current": value.Current, "active_power": value.ActivePower,
			"collect_time": compactTimestamp(value.Timestamp),
		}, nil
	case protocol.SwitchStatus:
		return map[string]any{
			"meter_id":     value.MeterID,
			"di1":          binaryState(value.DigitalInputs.Channel1),
			"di2":          binaryState(value.DigitalInputs.Channel2),
			"do1":          binaryState(value.DigitalOutputs.Channel1),
			"do2":          binaryState(value.DigitalOutputs.Channel2),
			"collect_time": compactTimestamp(value.Timestamp),
		}, nil
	default:
		return nil, fmt.Errorf("不能转换为Python电表属性上报：%T", message)
	}
}

func buildFrames(sessionID uint16, payload []byte, chunkSize int) ([][]byte, error) {
	if chunkSize <= 0 {
		return nil, errors.New("MQTT chunk size must be greater than zero")
	}
	total := (len(payload) + chunkSize - 1) / chunkSize
	if total == 0 {
		return nil, errors.New("MQTT payload cannot be empty")
	}
	if total > int(^uint16(0)) {
		return nil, fmt.Errorf("MQTT frame count too large: %d", total)
	}

	frames := make([][]byte, 0, total)
	for index, start := 0, 0; start < len(payload); index, start = index+1, start+chunkSize {
		end := start + chunkSize
		if end > len(payload) {
			end = len(payload)
		}
		frameType := protocol.MQTTFrameMiddle
		switch {
		case total == 1:
			frameType = protocol.MQTTFrameSingle
		case index == 0:
			frameType = protocol.MQTTFrameFirst
		case index == total-1:
			frameType = protocol.MQTTFrameLast
		}
		frame, err := protocol.EncodeMQTTFrame(
			frameType, sessionID, uint16(index), uint16(total), payload[start:end])
		if err != nil {
			return nil, fmt.Errorf("生成MQTT帧%d失败: %w", index, err)
		}
		frames = append(frames, frame)
	}
	return frames, nil
}

func allFrameIndexes(frames [][]byte) []int {
	indexes := make([]int, len(frames))
	for index := range frames {
		indexes[index] = index
	}
	return indexes
}

func validateLossIndexes(loss []int, frameCount int) ([]int, error) {
	if len(loss) == 0 {
		return nil, errors.New("cmd=2没有提供loss分片列表")
	}
	seen := make(map[int]struct{}, len(loss))
	indexes := make([]int, 0, len(loss))
	for _, index := range loss {
		if index < 0 || index >= frameCount {
			return nil, fmt.Errorf("分片序号%d不在0~%d范围内", index, frameCount-1)
		}
		if _, exists := seen[index]; exists {
			continue
		}
		seen[index] = struct{}{}
		indexes = append(indexes, index)
	}
	return indexes, nil
}

func compactTimestamp(value string) string {
	parsed, err := time.ParseInLocation(internalTimeLayout, value, time.Local)
	if err != nil {
		return time.Now().Format(serverTimeLayout)
	}
	return parsed.Format(serverTimeLayout)
}

func binaryState(value bool) string {
	if value {
		return "1"
	}
	return "0"
}
