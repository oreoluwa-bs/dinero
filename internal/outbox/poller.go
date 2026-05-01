package outbox

import (
	"context"
	"log/slog"
	"time"

	"github.com/oreoluwa-bs/dinero/internal/queue"
	"github.com/oreoluwa-bs/dinero/internal/repository"
)

type Poller struct {
	store     repository.Queries
	publisher queue.Publisher
	logger    *slog.Logger
	interval  time.Duration
}

func NewPoller(store repository.Queries, publisher queue.Publisher, logger *slog.Logger, interval time.Duration) *Poller {
	return &Poller{
		store:     store,
		publisher: publisher,
		logger:    logger,
		interval:  interval,
	}
}

func (p *Poller) Start(ctx context.Context) {
	p.logger.Info("outbox poller started", slog.Duration("interval", p.interval))
	go func() {
		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				p.logger.Info("outbox poller shutting down")
				return
			case <-ticker.C:
				p.poll(ctx)
			}
		}
	}()
}

func (p *Poller) poll(ctx context.Context) {
	messages, err := p.store.GetUnsentOutboxMessages(ctx)
	if err != nil {
		p.logger.Error("failed to fetch unsent outbox messages", slog.String("error", err.Error()))
		return
	}
	if len(messages) == 0 {
		return
	}

	p.logger.Info("publishing outbox messages", slog.Int("count", len(messages)))

	for _, msg := range messages {
		if err := p.publisher.Publish(ctx, "", msg.Topic, msg.Payload); err != nil {
			p.logger.Error("failed to publish outbox message",
				slog.Int64("id", msg.ID),
				slog.String("topic", msg.Topic),
				slog.String("error", err.Error()),
			)
			if incErr := p.store.IncrementOutboxErrorCount(ctx, msg.ID); incErr != nil {
				p.logger.Error("failed to increment outbox error count",
					slog.Int64("id", msg.ID),
					slog.String("error", incErr.Error()),
				)
			}
			continue
		}

		if err := p.store.MarkOutboxMessageSent(ctx, msg.ID); err != nil {
			p.logger.Error("failed to mark outbox message as sent",
				slog.Int64("id", msg.ID),
				slog.String("error", err.Error()),
			)
			continue
		}

		p.logger.Info("outbox message published",
			slog.Int64("id", msg.ID),
			slog.String("topic", msg.Topic),
		)
	}
}
