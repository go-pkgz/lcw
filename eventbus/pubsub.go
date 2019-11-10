package eventbus

type PubSub interface {
	Publish(fromID string, key string) error
	Subscribe(fn func(fromID string, key string)) error
}

// NopPubSub implements default do-nothing pub-sub (event bus)
type NopPubSub struct{}

func (n *NopPubSub) Subscribe(fn func(fromID string, key string)) error {
	return nil
}

func (n *NopPubSub) Publish(fromID string, key string) error {
	return nil
}
