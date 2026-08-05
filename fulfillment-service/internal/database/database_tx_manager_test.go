package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Transaction manager", func() {
	var (
		ctx  context.Context
		pool *pgxpool.Pool
	)

	BeforeEach(func() {
		var err error

		ctx = context.Background()

		db, err := server.NewInstance().Build()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(db.Close)
		pool, err = db.Pool(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(pool.Close)
	})

	Describe("Build", func() {
		It("Should return an error if logger is not set", func() {
			_, err := NewTxManager().
				SetPool(pool).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
		})

		It("Should return an error if pool is not set", func() {
			_, err := NewTxManager().
				SetLogger(logger).
				Build()
			Expect(err).To(MatchError("database connection pool is mandatory"))
		})

		It("Should succeed when all parameters are set", func() {
			manager, err := NewTxManager().
				SetLogger(logger).
				SetPool(pool).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(manager).NotTo(BeNil())
		})
	})

	Describe("Begin transaction", func() {
		var manager TxManager

		BeforeEach(func() {
			var err error
			manager, err = NewTxManager().
				SetLogger(logger).
				SetPool(pool).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Should create a new managed transaction", func() {
			tx, err := manager.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(tx).NotTo(BeNil())
		})
	})

	Describe("End transaction", func() {
		var manager TxManager

		BeforeEach(func() {
			var err error
			manager, err = NewTxManager().
				SetLogger(logger).
				SetPool(pool).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Should commit the transaction if no errors are reported", func() {
			// Create a table:
			_, err := pool.Exec(ctx, "create table my_table (my_column text)")
			Expect(err).ToNot(HaveOccurred())

			// Do some changes inside a transaction:
			func() {
				tx, err := manager.Begin(ctx)
				Expect(err).ToNot(HaveOccurred())
				defer func() {
					err := tx.End(ctx)
					Expect(err).ToNot(HaveOccurred())
				}()
				_, err = tx.Exec(ctx, "insert into my_table (my_column) values ($1)", "my_value")
				Expect(err).ToNot(HaveOccurred())
			}()

			// Verify that the changes are still there:
			row := pool.QueryRow(ctx, "select my_column from my_table")
			var value string
			err = row.Scan(&value)
			Expect(err).ToNot(HaveOccurred())
			Expect(value).To(Equal("my_value"))
		})

		It("Should rollback the transaction if errors are reported", func() {
			// Create a table:
			_, err := pool.Exec(ctx, "create table my_table (my_column text)")
			Expect(err).ToNot(HaveOccurred())

			// Do some changes inside a transaction, and report an error:
			func() {
				tx, err := manager.Begin(ctx)
				Expect(err).ToNot(HaveOccurred())
				defer func() {
					err := tx.End(ctx)
					Expect(err).ToNot(HaveOccurred())
				}()
				_, err = tx.Exec(ctx, "insert into my_table (my_column) values ($1)", "my_value")
				Expect(err).ToNot(HaveOccurred())
				err = errors.New("my error")
				tx.ReportError(&err)
			}()

			// Verify that the changes aren't there:
			row := pool.QueryRow(ctx, "select my_column from my_table")
			var value string
			err = row.Scan(&value)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("Run", func() {
		var manager TxManager

		BeforeEach(func() {
			var err error
			manager, err = NewTxManager().
				SetLogger(logger).
				SetPool(pool).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = pool.Exec(ctx, "create table my_table (my_column text)")
			Expect(err).ToNot(HaveOccurred())
		})

		It("Should accept 'func(context.Context)' and commit on success", func() {
			err := manager.Run(ctx, func(ctx context.Context) {
				tx, err := TxFromContext(ctx)
				Expect(err).ToNot(HaveOccurred())
				_, err = tx.Exec(ctx, "insert into my_table (my_column) values ($1)", "ctx_void")
				Expect(err).ToNot(HaveOccurred())
			})
			Expect(err).ToNot(HaveOccurred())

			row := pool.QueryRow(ctx, "select my_column from my_table")
			var value string
			err = row.Scan(&value)
			Expect(err).ToNot(HaveOccurred())
			Expect(value).To(Equal("ctx_void"))
		})

		It("Should accept a 'func(context.Context) error' and commit when nil is returned", func() {
			err := manager.Run(ctx, func(ctx context.Context) error {
				tx, err := TxFromContext(ctx)
				if err != nil {
					return err
				}
				_, err = tx.Exec(ctx, "insert into my_table (my_column) values ($1)", "ctx_err")
				return err
			})
			Expect(err).ToNot(HaveOccurred())

			row := pool.QueryRow(ctx, "select my_column from my_table")
			var value string
			err = row.Scan(&value)
			Expect(err).ToNot(HaveOccurred())
			Expect(value).To(Equal("ctx_err"))
		})

		It("Should accept a 'func(context.Context) error' and rollback when an error is returned", func() {
			taskErr := fmt.Errorf("something went wrong")
			err := manager.Run(ctx, func(ctx context.Context) error {
				tx, err := TxFromContext(ctx)
				if err != nil {
					return err
				}
				_, err = tx.Exec(ctx, "insert into my_table (my_column) values ($1)", "ctx_err_fail")
				Expect(err).ToNot(HaveOccurred())
				return taskErr
			})
			Expect(err).To(BeIdenticalTo(taskErr))

			row := pool.QueryRow(ctx, "select my_column from my_table")
			var value string
			err = row.Scan(&value)
			Expect(err).To(HaveOccurred())
		})

		It("Should accept a 'func(Tx)' and commit on success", func() {
			err := manager.Run(ctx, func(tx Tx) {
				_, err := tx.Exec(ctx, "insert into my_table (my_column) values ($1)", "tx_void")
				Expect(err).ToNot(HaveOccurred())
			})
			Expect(err).ToNot(HaveOccurred())

			row := pool.QueryRow(ctx, "select my_column from my_table")
			var value string
			err = row.Scan(&value)
			Expect(err).ToNot(HaveOccurred())
			Expect(value).To(Equal("tx_void"))
		})

		It("Should accept a 'func(Tx) error' and commit when nil is returned", func() {
			err := manager.Run(ctx, func(tx Tx) error {
				_, err := tx.Exec(ctx, "insert into my_table (my_column) values ($1)", "tx_err")
				return err
			})
			Expect(err).ToNot(HaveOccurred())

			row := pool.QueryRow(ctx, "select my_column from my_table")
			var value string
			err = row.Scan(&value)
			Expect(err).ToNot(HaveOccurred())
			Expect(value).To(Equal("tx_err"))
		})

		It("Should accept a 'func(Tx) error' and rollback when an error is returned", func() {
			taskErr := fmt.Errorf("tx failed")
			err := manager.Run(ctx, func(tx Tx) error {
				_, err := tx.Exec(ctx, "insert into my_table (my_column) values ($1)", "tx_err_fail")
				Expect(err).ToNot(HaveOccurred())
				return taskErr
			})
			Expect(err).To(BeIdenticalTo(taskErr))

			row := pool.QueryRow(ctx, "select my_column from my_table")
			var value string
			err = row.Scan(&value)
			Expect(err).To(HaveOccurred())
		})

		It("Should rollback when the task panics", func() {
			err := manager.Run(ctx, func(tx Tx) error {
				_, err := tx.Exec(ctx, "insert into my_table (my_column) values ($1)", "panic_val")
				Expect(err).ToNot(HaveOccurred())
				panic("unexpected failure")
			})
			Expect(err).To(MatchError(ContainSubstring("task panicked")))
			Expect(err).To(MatchError(ContainSubstring("unexpected failure")))

			row := pool.QueryRow(ctx, "select my_column from my_table")
			var value string
			err = row.Scan(&value)
			Expect(err).To(HaveOccurred())
		})

		It("Should rollback when 'ReportError' is called inside the task", func() {
			err := manager.Run(ctx, func(ctx context.Context) {
				tx, err := TxFromContext(ctx)
				Expect(err).ToNot(HaveOccurred())
				_, err = tx.Exec(ctx, "insert into my_table (my_column) values ($1)", "report_err")
				Expect(err).ToNot(HaveOccurred())
				reportedErr := fmt.Errorf("reported error")
				tx.ReportError(&reportedErr)
			})
			Expect(err).ToNot(HaveOccurred())

			row := pool.QueryRow(ctx, "select my_column from my_table")
			var value string
			err = row.Scan(&value)
			Expect(err).To(HaveOccurred())
		})

		It("Should pass extra arguments to the task function", func() {
			err := manager.Run(
				ctx,
				func(tx Tx, table string, value string) error {
					_, err := tx.Exec(
						ctx,
						fmt.Sprintf("insert into %s (my_column) values ($1)", table),
						value,
					)
					return err
				},
				"my_table",
				"extra_args",
			)
			Expect(err).ToNot(HaveOccurred())

			row := pool.QueryRow(ctx, "select my_column from my_table")
			var value string
			err = row.Scan(&value)
			Expect(err).ToNot(HaveOccurred())
			Expect(value).To(Equal("extra_args"))
		})

		It("Should accept a task with multiple return values where the last is error", func() {
			err := manager.Run(ctx, func(tx Tx, val string) (int, error) {
				_, err := tx.Exec(ctx, "insert into my_table (my_column) values ($1)", val)
				return 42, err
			}, "multi_return")
			Expect(err).ToNot(HaveOccurred())

			row := pool.QueryRow(ctx, "select my_column from my_table")
			var value string
			err = row.Scan(&value)
			Expect(err).ToNot(HaveOccurred())
			Expect(value).To(Equal("multi_return"))
		})

		It("Should accept a task with return values where the last is not error", func() {
			err := manager.Run(ctx, func(tx Tx) int {
				_, txErr := tx.Exec(ctx, "insert into my_table (my_column) values ($1)", "no_err_return")
				Expect(txErr).ToNot(HaveOccurred())
				return 7
			})
			Expect(err).ToNot(HaveOccurred())

			row := pool.QueryRow(ctx, "select my_column from my_table")
			var value string
			err = row.Scan(&value)
			Expect(err).ToNot(HaveOccurred())
			Expect(value).To(Equal("no_err_return"))
		})

		It("Should return an error if task is not a function", func() {
			err := manager.Run(ctx, "not a function")
			Expect(err).To(MatchError(ContainSubstring("task must be a function")))
		})

		It("Should return an error if first parameter is not context or Tx", func() {
			err := manager.Run(ctx, func(s string) {})
			Expect(err).To(MatchError(ContainSubstring("first parameter of task function must be")))
		})

		It("Should return an error if argument count does not match", func() {
			err := manager.Run(ctx, func(tx Tx, a string, b int) error {
				return nil
			}, "only one arg")
			Expect(err).To(MatchError(ContainSubstring("expects 2 additional argument(s), got 1")))
		})

		It("Should handle nil arguments", func() {
			type config struct{ Name string }
			err := manager.Run(ctx, func(tx Tx, cfg *config) error {
				Expect(cfg).To(BeNil())
				_, err := tx.Exec(ctx, "insert into my_table (my_column) values ($1)", "nil_arg")
				return err
			}, nil)
			Expect(err).ToNot(HaveOccurred())

			row := pool.QueryRow(ctx, "select my_column from my_table")
			var value string
			err = row.Scan(&value)
			Expect(err).ToNot(HaveOccurred())
			Expect(value).To(Equal("nil_arg"))
		})

		It("Should return an error if nil is passed for a non-nilable parameter", func() {
			err := manager.Run(ctx, func(tx Tx, n int) error {
				return nil
			}, nil)
			Expect(err).To(MatchError(ContainSubstring(
				"argument 1 is nil but parameter type int is not nilable",
			)))
		})

		It("Should return an error if argument type does not match parameter type", func() {
			err := manager.Run(ctx, func(tx Tx, n int) error {
				return nil
			}, "not an int")
			Expect(err).To(MatchError(ContainSubstring(
				"argument 1 has type string which is not assignable to parameter type int",
			)))
		})
	})

	Describe("Transaction run", func() {
		var manager TxManager

		BeforeEach(func() {
			var err error
			manager, err = NewTxManager().
				SetLogger(logger).
				SetPool(pool).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = pool.Exec(ctx, "create table my_table (my_column text)")
			Expect(err).ToNot(HaveOccurred())
		})

		It("Should commit when task succeeds", func() {
			tx, err := manager.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())

			err = tx.Run(ctx, func(tx Tx) error {
				_, err := tx.Exec(ctx, "insert into my_table (my_column) values ($1)", "tx_run_ok")
				return err
			})
			Expect(err).ToNot(HaveOccurred())

			err = tx.End(ctx)
			Expect(err).ToNot(HaveOccurred())

			row := pool.QueryRow(ctx, "select my_column from my_table")
			var value string
			err = row.Scan(&value)
			Expect(err).ToNot(HaveOccurred())
			Expect(value).To(Equal("tx_run_ok"))
		})

		It("Should report the error and rollback when task returns an error", func() {
			tx, err := manager.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())

			taskErr := fmt.Errorf("task failed")
			err = tx.Run(ctx, func(tx Tx) error {
				_, err := tx.Exec(ctx, "insert into my_table (my_column) values ($1)", "tx_run_err")
				Expect(err).ToNot(HaveOccurred())
				return taskErr
			})
			Expect(err).To(BeIdenticalTo(taskErr))

			err = tx.End(ctx)
			Expect(err).ToNot(HaveOccurred())

			row := pool.QueryRow(ctx, "select my_column from my_table")
			var value string
			err = row.Scan(&value)
			Expect(err).To(HaveOccurred())
		})

		It("Should report the error and rollback when task panics", func() {
			tx, err := manager.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())

			err = tx.Run(ctx, func(tx Tx) error {
				_, err := tx.Exec(ctx, "insert into my_table (my_column) values ($1)", "tx_run_panic")
				Expect(err).ToNot(HaveOccurred())
				panic("boom")
			})
			Expect(err).To(MatchError(ContainSubstring("task panicked")))
			Expect(err).To(MatchError(ContainSubstring("boom")))

			err = tx.End(ctx)
			Expect(err).ToNot(HaveOccurred())

			row := pool.QueryRow(ctx, "select my_column from my_table")
			var value string
			err = row.Scan(&value)
			Expect(err).To(HaveOccurred())
		})

		It("Should pass extra arguments to the task", func() {
			tx, err := manager.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())

			err = tx.Run(ctx, func(tx Tx, value string) error {
				_, err := tx.Exec(ctx, "insert into my_table (my_column) values ($1)", value)
				return err
			}, "tx_run_args")
			Expect(err).ToNot(HaveOccurred())

			err = tx.End(ctx)
			Expect(err).ToNot(HaveOccurred())

			row := pool.QueryRow(ctx, "select my_column from my_table")
			var value string
			err = row.Scan(&value)
			Expect(err).ToNot(HaveOccurred())
			Expect(value).To(Equal("tx_run_args"))
		})

		It("Should accept a context-based task", func() {
			tx, err := manager.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())

			err = tx.Run(ctx, func(ctx context.Context) error {
				innerTx, err := TxFromContext(ctx)
				if err != nil {
					return err
				}
				_, err = innerTx.Exec(ctx, "insert into my_table (my_column) values ($1)", "tx_run_ctx")
				return err
			})
			Expect(err).ToNot(HaveOccurred())

			err = tx.End(ctx)
			Expect(err).ToNot(HaveOccurred())

			row := pool.QueryRow(ctx, "select my_column from my_table")
			var value string
			err = row.Scan(&value)
			Expect(err).ToNot(HaveOccurred())
			Expect(value).To(Equal("tx_run_ctx"))
		})
	})
})
