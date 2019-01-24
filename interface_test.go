package lcw

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNop_Get(t *testing.T) {
	var coldCalls int32
	c := NewNopCache()
	res, err := c.Get("key1", func() (Value, error) {
		atomic.AddInt32(&coldCalls, 1)
		return "result", nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "result", res.(string))
	assert.Equal(t, int32(1), atomic.LoadInt32(&coldCalls))

	res, err = c.Get("key1", func() (Value, error) {
		atomic.AddInt32(&coldCalls, 1)
		return "result2", nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "result2", res.(string))
	assert.Equal(t, int32(2), atomic.LoadInt32(&coldCalls))
}

func TestNop_Peek(t *testing.T) {
	var coldCalls int32
	c := NewNopCache()
	res, err := c.Get("key1", func() (Value, error) {
		atomic.AddInt32(&coldCalls, 1)
		return "result", nil
	})
	assert.Nil(t, err)
	assert.Equal(t, "result", res.(string))
	assert.Equal(t, int32(1), atomic.LoadInt32(&coldCalls))

	_, ok := c.Peek("key1")
	assert.False(t, ok)
}
