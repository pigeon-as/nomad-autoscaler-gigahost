// Copyright (c) pigeon-as
// SPDX-License-Identifier: MPL-2.0

package plugin

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shoenig/test/must"
)

func TestRetry(t *testing.T) {
	t.Parallel()

	t.Run("successful function first time", func(t *testing.T) {
		err := retry(context.Background(), time.Millisecond, 1, func(ctx context.Context) (bool, error) {
			return true, nil
		})
		must.NoError(t, err)
	})

	t.Run("function never successful and reaches retry limit", func(t *testing.T) {
		err := retry(context.Background(), time.Millisecond, 2, func(ctx context.Context) (bool, error) {
			return false, errors.New("error")
		})
		must.EqError(t, err, "reached retry limit")
	})

	t.Run("stop with terminal error", func(t *testing.T) {
		err := retry(context.Background(), time.Millisecond, 5, func(ctx context.Context) (bool, error) {
			return true, errors.New("terminal")
		})
		must.EqError(t, err, "terminal")
	})

	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := retry(ctx, time.Millisecond, 5, func(ctx context.Context) (bool, error) {
			return false, errors.New("error")
		})
		must.Error(t, err)
	})
}
