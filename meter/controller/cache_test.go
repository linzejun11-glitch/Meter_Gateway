package controller

import (
	"testing"

	"MOCK_COLLECT/common/protocol"
)

// TestACKCacheEvictsOldest 验证缓存超过容量后会删除最早的记录。
func TestACKCacheEvictsOldest(t *testing.T) {
	cache := newACKCache(2)

	cache.put("cmd-1", protocol.ControlACK{RequestID: "cmd-1"})
	cache.put("cmd-2", protocol.ControlACK{RequestID: "cmd-2"})
	cache.put("cmd-3", protocol.ControlACK{RequestID: "cmd-3"})

	if _, exists := cache.get("cmd-1"); exists {
		t.Fatal("超过容量后，最早的cmd-1应该被删除")
	}
	if _, exists := cache.get("cmd-2"); !exists {
		t.Fatal("cmd-2应该仍在缓存中")
	}
	if ack, exists := cache.get("cmd-3"); !exists || ack.RequestID != "cmd-3" {
		t.Fatal("最新的cmd-3应该可以从缓存读取")
	}
}
