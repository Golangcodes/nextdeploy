package serverless

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/Golangcodes/nextdeploy/shared/config"

	"github.com/cloudflare/cloudflare-go/v6"
	"github.com/cloudflare/cloudflare-go/v6/queues"
)

// ensureQueue creates a Queue with the given name if one doesn't already
// exist on the account. Returns the CF queue ID.
//
// CF queue names are unique per account. List + match-by-name is the only way
// to detect existence — there is no "get by name" endpoint.
func (p *CloudflareProvider) ensureQueue(ctx context.Context, name string) (string, error) {
	id, err := p.findQueueID(ctx, name)
	if err != nil {
		return "", fmt.Errorf("list queues: %w", err)
	}
	if id != "" {
		p.log.Info("Queue already exists: %s (id=%s)", name, id)
		return id, nil
	}
	created, err := p.cf.Queues.New(ctx, queues.QueueNewParams{
		AccountID: cloudflare.F(p.accountID),
		QueueName: cloudflare.F(name),
	})
	if err != nil {
		return "", fmt.Errorf("create queue %q: %w", name, err)
	}
	p.log.Info("Queue created: %s (id=%s)", name, created.QueueID)
	return created.QueueID, nil
}

// findQueueID returns the ID of the queue with the given name, or "" if none
// exists. Other errors propagate.
func (p *CloudflareProvider) findQueueID(ctx context.Context, name string) (string, error) {
	iter := p.cf.Queues.ListAutoPaging(ctx, queues.QueueListParams{
		AccountID: cloudflare.F(p.accountID),
	})
	for iter.Next() {
		q := iter.Current()
		if q.QueueName == name {
			return q.QueueID, nil
		}
	}
	if err := iter.Err(); err != nil {
		var apiErr *cloudflare.Error
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return "", nil
		}
		return "", err
	}
	return "", nil
}

// ensureQueueConsumer wires a worker as the consumer of a queue, with optional
// DLQ + retry settings. Idempotent: lists existing consumers for the queue,
// updates if one matches scriptName, otherwise creates.
//
// queueName resolves to its ID via findQueueID — caller may pass either the
// consumer's source queue's name or look it up themselves first.
func (p *CloudflareProvider) ensureQueueConsumer(ctx context.Context, scriptName string, c config.CFQueueConsumer) error {
	queueID, err := p.findQueueID(ctx, c.Queue)
	if err != nil {
		return fmt.Errorf("resolve queue %q: %w", c.Queue, err)
	}
	if queueID == "" {
		return fmt.Errorf("consumer for queue %q: queue does not exist (declare it under cloudflare.resources.queues)", c.Queue)
	}

	settings := queues.ConsumerNewParamsBodyMqWorkerConsumerRequestSettings{}
	if c.MaxRetries > 0 {
		settings.MaxRetries = cloudflare.F(float64(c.MaxRetries))
	}
	if c.MaxBatchSize > 0 {
		settings.BatchSize = cloudflare.F(float64(c.MaxBatchSize))
	}
	if c.MaxBatchTimeout > 0 {
		settings.MaxWaitTimeMs = cloudflare.F(float64(c.MaxBatchTimeout * 1000))
	}

	body := queues.ConsumerNewParamsBodyMqWorkerConsumerRequest{
		ScriptName: cloudflare.F(scriptName),
		Type:       cloudflare.F(queues.ConsumerNewParamsBodyMqWorkerConsumerRequestTypeWorker),
		Settings:   cloudflare.F(settings),
	}
	if c.DeadLetterQueue != "" {
		body.DeadLetterQueue = cloudflare.F(c.DeadLetterQueue)
	}

	existingID, err := p.findConsumerID(ctx, queueID, scriptName)
	if err != nil {
		return fmt.Errorf("list consumers for %q: %w", c.Queue, err)
	}
	if existingID != "" {
		updateBody := queues.ConsumerUpdateParamsBodyMqWorkerConsumerRequest{
			ScriptName: cloudflare.F(scriptName),
			Type:       cloudflare.F(queues.ConsumerUpdateParamsBodyMqWorkerConsumerRequestTypeWorker),
			Settings: cloudflare.F(queues.ConsumerUpdateParamsBodyMqWorkerConsumerRequestSettings{
				MaxRetries:    settings.MaxRetries,
				BatchSize:     settings.BatchSize,
				MaxWaitTimeMs: settings.MaxWaitTimeMs,
			}),
		}
		if c.DeadLetterQueue != "" {
			updateBody.DeadLetterQueue = cloudflare.F(c.DeadLetterQueue)
		}
		_, err := p.cf.Queues.Consumers.Update(ctx, queueID, existingID, queues.ConsumerUpdateParams{
			AccountID: cloudflare.F(p.accountID),
			Body:      updateBody,
		})
		if err != nil {
			return fmt.Errorf("update consumer for %q: %w", c.Queue, err)
		}
		p.log.Info("Queue consumer updated: %s ← %s (dlq=%s)", c.Queue, scriptName, c.DeadLetterQueue)
		return nil
	}

	if _, err := p.cf.Queues.Consumers.New(ctx, queueID, queues.ConsumerNewParams{
		AccountID: cloudflare.F(p.accountID),
		Body:      body,
	}); err != nil {
		return fmt.Errorf("create consumer for %q: %w", c.Queue, err)
	}
	p.log.Info("Queue consumer created: %s ← %s (dlq=%s)", c.Queue, scriptName, c.DeadLetterQueue)
	return nil
}

// findConsumerID returns the consumer ID that targets scriptName for queueID,
// or "" if none.
func (p *CloudflareProvider) findConsumerID(ctx context.Context, queueID, scriptName string) (string, error) {
	iter := p.cf.Queues.Consumers.ListAutoPaging(ctx, queueID, queues.ConsumerListParams{
		AccountID: cloudflare.F(p.accountID),
	})
	for iter.Next() {
		c := iter.Current()
		if c.ScriptName == scriptName {
			return c.ConsumerID, nil
		}
	}
	if err := iter.Err(); err != nil {
		var apiErr *cloudflare.Error
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return "", nil
		}
		return "", err
	}
	return "", nil
}
