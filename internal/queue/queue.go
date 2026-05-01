package queue

import (
	"context"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type Publisher interface {
	Publish(ctx context.Context, exchange, routingKey string, body []byte) error
}

type Consumer interface {
	Start(ctx context.Context, queue string, handler func(ctx context.Context, body []byte) error) error
}

type Queue struct {
	conn       *amqp.Connection
	ch         *amqp.Channel
	tracer     trace.Tracer
	propagator propagation.TextMapPropagator
}

func New(url string, tracer trace.Tracer) (*Queue, error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("rabbitmq connect: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("rabbitmq channel: %w", err)
	}

	return &Queue{
		conn:   conn,
		ch:     ch,
		tracer: tracer,
		propagator: propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
	}, nil
}

func (c *Queue) Publish(ctx context.Context, exchange, routingKey string, body []byte) error {
	headers := amqp.Table{}
	c.propagator.Inject(ctx, amqpHeadersCarrier(headers))

	return c.ch.PublishWithContext(ctx,
		exchange,
		routingKey,
		false,
		false,
		amqp.Publishing{
			ContentType: "application/json",
			Body:        body,
			Headers:     headers,
		})
}

func (c *Queue) Start(ctx context.Context, queueName string, handler func(ctx context.Context, body []byte) error) error {
	c.ch.QueueDeclare(queueName, true, false, false, false, nil)
	msgs, err := c.ch.ConsumeWithContext(ctx, queueName, "", false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("consume: %w", err)
	}

	go func() {
		for d := range msgs {
			msgCtx := c.propagator.Extract(ctx, amqpHeadersCarrier(d.Headers))

			spanCtx, span := c.tracer.Start(msgCtx, "queue.consume", trace.WithAttributes(
				attribute.String("messaging.system", "rabbitmq"),
				attribute.String("messaging.destination", queueName),
				attribute.String("messaging.message_id", d.MessageId),
			))
			defer span.End()

			if err := handler(spanCtx, d.Body); err != nil {
				span.RecordError(err)
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

// amqpHeadersCarrier adapts amqp.Table to the TextMapCarrier interface
type amqpHeadersCarrier amqp.Table

func (c amqpHeadersCarrier) Get(key string) string {
	v, ok := c[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func (c amqpHeadersCarrier) Set(key string, value string) {
	c[key] = value
}

func (c amqpHeadersCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}
