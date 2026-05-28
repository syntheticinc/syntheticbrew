package configrepo

import (
	"context"

	"gorm.io/gorm"
)

// txCtxKey is the type-safe key under which GORMTransactionRunner stashes
// the active transaction-bound *gorm.DB. Repositories that participate in
// usecase-managed transactions call txFromContext to pick up that handle.
type txCtxKey struct{}

// WithTx attaches a transaction-bound *gorm.DB to the context. Called by
// GORMTransactionRunner.InTransaction before invoking the wrapped function.
func WithTx(ctx context.Context, tx *gorm.DB) context.Context {
	return context.WithValue(ctx, txCtxKey{}, tx)
}

// txFromContext returns the transaction-bound *gorm.DB if the context carries
// one. Repositories fall back to their base handle when the second return
// value is false.
func txFromContext(ctx context.Context) (*gorm.DB, bool) {
	tx, ok := ctx.Value(txCtxKey{}).(*gorm.DB)
	return tx, ok
}

// GORMTransactionRunner satisfies the TransactionRunner consumer interfaces
// of kgapply and kgmutate. It wraps the engine's base *gorm.DB and runs the
// user function inside db.Transaction, threading the tx handle through
// context so repositories see the same transaction.
type GORMTransactionRunner struct {
	db *gorm.DB
}

// NewGORMTransactionRunner constructs a runner backed by the given handle.
func NewGORMTransactionRunner(db *gorm.DB) *GORMTransactionRunner {
	return &GORMTransactionRunner{db: db}
}

// InTransaction runs fn inside a database transaction. The transaction handle
// is attached to ctx via WithTx so any repository call inside fn picks it up.
func (r *GORMTransactionRunner) InTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(WithTx(ctx, tx))
	})
}
