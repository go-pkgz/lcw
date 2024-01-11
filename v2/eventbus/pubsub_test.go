package eventbus

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNopPubSub(t *testing.T) {
	nopPubSub := NopPubSub{}
	assert.NoError(t, nopPubSub.Subscribe(nil))
	assert.NoError(t, nopPubSub.Publish("", ""))
}
