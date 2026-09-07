package controller

import "MOCK_COLLECT/common/protocol"

// ackCache 保存最近处理过的控制命令结果。
//
// ACK是Acknowledgement的缩写，表示“确认应答”。
// 当服务器重复发送相同request_id时，客户端直接返回历史ACK，
// 不会让继电器重复动作。
type ackCache struct {
	maxEntries int
	values     map[string]protocol.ControlACK
	order      []string
}

// newACKCache 创建一个最多保存maxEntries条记录的缓存。
func newACKCache(maxEntries int) *ackCache {
	return &ackCache{
		maxEntries: maxEntries,
		values:     make(map[string]protocol.ControlACK),
		order:      make([]string, 0, maxEntries),
	}
}

// get 按request_id查找历史ACK。
func (c *ackCache) get(requestID string) (protocol.ControlACK, bool) {
	if requestID == "" {
		return protocol.ControlACK{}, false
	}

	ack, exists := c.values[requestID]
	return ack, exists
}

// put 保存ACK，并在超过容量时删除最早的记录。
func (c *ackCache) put(requestID string, ack protocol.ControlACK) {
	if requestID == "" {
		return
	}

	c.values[requestID] = ack
	c.order = append(c.order, requestID)

	if len(c.order) <= c.maxEntries {
		return
	}

	oldest := c.order[0]
	c.order = c.order[1:]
	delete(c.values, oldest)
}
