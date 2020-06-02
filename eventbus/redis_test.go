package eventbus

import (
	"math/rand"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRedisPubSub_Error(t *testing.T) {
	redisPubSub, err := NewRedisPubSub("127.0.0.1:99999", "test")
	require.Error(t, err)
	require.Nil(t, redisPubSub)
}

func TestRedisPubSub(t *testing.T) {
	if _, ok := os.LookupEnv("ENABLE_REDIS_TESTS"); !ok {
		t.Skip("ENABLE_REDIS_TESTS env variable is not set, not expecting Redis to be ready at 127.0.0.1:6379")
	}

	channel := "lcw-test-" + strconv.Itoa(rand.Intn(1000000))
	redisPubSub, err := NewRedisPubSub("127.0.0.1:6379", channel)
	require.NoError(t, err)
	require.NotNil(t, redisPubSub)
	var called []string
	assert.Nil(t, redisPubSub.Subscribe(func(fromID string, key string) {
		called = append(called, fromID, key)
	}))
	assert.NoError(t, redisPubSub.Publish("test_fromID", "test_key"))
	// Sleep which waits for Subscribe goroutine to pick up published changes
	time.Sleep(time.Second)
	assert.NoError(t, redisPubSub.Close())
	assert.Equal(t, []string{"test_fromID", "test_key"}, called)
}
