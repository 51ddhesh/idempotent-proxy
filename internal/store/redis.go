package store

import (
	"context"
	"fmt"
	"github.com/redis/go-redis/v9"
	"time"
)

func NewRedisClient(addr string) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr: addr,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := client.Ping(ctx).Result(); err != nil {
		return nil, err
	}

	fmt.Println("[STORE]: Redis Connection established")
	return client, nil
}
