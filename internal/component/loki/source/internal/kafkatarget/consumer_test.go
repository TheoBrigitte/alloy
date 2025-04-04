package kafkatarget

// This code is copied from Promtail (https://github.com/grafana/loki/commit/065bee7e72b00d800431f4b70f0d673d6e0e7a2b). The kafkatarget package is used to
// configure and run the targets that can read kafka entries and forward them
// to other loki components.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/go-kit/log"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/require"

	"github.com/grafana/loki/v3/clients/pkg/promtail/targets/target"
)

type DiscovererFn func(sarama.ConsumerGroupSession, sarama.ConsumerGroupClaim) (RunnableTarget, error)

func (d DiscovererFn) NewTarget(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) (RunnableTarget, error) {
	return d(session, claim)
}

type fakeTarget struct {
	ctx context.Context
	lbs model.LabelSet
}

func (f *fakeTarget) run()                             { <-f.ctx.Done() }
func (f *fakeTarget) Type() target.TargetType          { return "" }
func (f *fakeTarget) DiscoveredLabels() model.LabelSet { return nil }
func (f *fakeTarget) Labels() model.LabelSet           { return f.lbs }
func (f *fakeTarget) Ready() bool                      { return true }
func (f *fakeTarget) Details() interface{}             { return nil }

func Test_ConsumerConsume(t *testing.T) {
	var (
		group       = &testConsumerGroupHandler{}
		session     = &testSession{}
		ctx, cancel = context.WithCancel(t.Context())
		c           = &consumer{
			logger:        log.NewNopLogger(),
			ctx:           t.Context(),
			cancel:        func() {},
			ConsumerGroup: group,
			discoverer: DiscovererFn(func(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) (RunnableTarget, error) {
				if claim.Topic() != "dropped" {
					return &fakeTarget{
						ctx: ctx,
						lbs: model.LabelSet{"topic": model.LabelValue(claim.Topic())},
					}, nil
				}
				return &fakeTarget{
					ctx: ctx,
				}, nil
			}),
		}
	)

	c.start(ctx, []string{"foo"})
	require.Eventually(t, group.consuming.Load, 5*time.Second, 100*time.Microsecond)
	require.NoError(t, group.handler.Setup(session))
	go func() {
		err := group.handler.ConsumeClaim(session, newTestClaim("foo", 1, 2))
		require.NoError(t, err)
	}()
	go func() {
		err := group.handler.ConsumeClaim(session, newTestClaim("bar", 1, 2))
		require.NoError(t, err)
	}()
	go func() {
		err := group.handler.ConsumeClaim(session, newTestClaim("dropped", 1, 2))
		require.NoError(t, err)
	}()
	require.Eventually(t, func() bool {
		return len(c.getActiveTargets()) == 2
	}, 2*time.Second, 100*time.Millisecond)
	require.Eventually(t, func() bool {
		return len(c.getDroppedTargets()) == 1
	}, 2*time.Second, 100*time.Millisecond)
	err := group.handler.Cleanup(session)
	require.NoError(t, err)
	cancel()
	c.stop()
}

func Test_ConsumerRetry(t *testing.T) {
	var (
		group = &testConsumerGroupHandler{
			returnErr: errors.New("foo"),
		}
		ctx, cancel = context.WithCancel(t.Context())
		c           = &consumer{
			logger:        log.NewNopLogger(),
			ctx:           t.Context(),
			cancel:        func() {},
			ConsumerGroup: group,
		}
	)
	defer cancel()
	c.start(ctx, []string{"foo"})
	<-time.After(2 * time.Second)
	c.stop()
}
