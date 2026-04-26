package queue

import (
	"context"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
)

type Publisher interface {
	Publish(ctx context.Context, exchange, routingKey string, body []byte) error
}

type Consumer interface {
	Start(ctx context.Context, queue string, handler func(ctx context.Context, body []byte) error) error
}

type Queue struct {
	conn *amqp.Connection
	ch   *amqp.Channel
}

func New(url string) (*Queue, error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("rabbitmq connect: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("rabbitmq channel: %w", err)
	}

	return &Queue{conn: conn, ch: ch}, nil
}

func (c *Queue) Publish(ctx context.Context, exchange, routingKey string, body []byte) error {
	return c.ch.PublishWithContext(ctx,
		exchange,
		routingKey,
		false,
		false,
		amqp.Publishing{
			ContentType: "application/json",
			Body:        body,
		})
}

func (c *Queue) Start(ctx context.Context, queue string, handler func(ctx context.Context, body []byte) error) error {
	c.ch.QueueDeclare(queue, true, false, false, false, nil)
	msgs, err := c.ch.ConsumeWithContext(ctx, queue, "", false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("consume: %w", err)
	}

	go func() {
		for d := range msgs {
			if err := handler(ctx, d.Body); err != nil {
				d.Nack(false, true) // requeue on failure
				continue
			}
			d.Ack(false)
		}
	}()
	return nil
}

func (c *Queue) Close() error {
	if c.ch != nil {
		c.ch.Close()
	}
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
