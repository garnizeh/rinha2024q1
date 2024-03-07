package main

import (
	"context"
	"time"

	"github.com/goccy/go-json"
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	db, err := pgxpool.New(
		context.Background(),
		"host=postgres user=postgres password=postgres dbname=postgres port=5432 sslmode=disable",
	)
	if err != nil {
		panic(err)
	}

	defer db.Close()

	app := fiber.New(fiber.Config{
		Prefork:               false,
		ReadTimeout:           time.Millisecond * 1000,
		WriteTimeout:          time.Millisecond * 1000,
		JSONEncoder:           json.Marshal,
		JSONDecoder:           json.Unmarshal,
		DisableStartupMessage: true,
		AppName:               "garnizeh",
	})

	app.Get("/clientes/:id/extrato", getExtrato(db))
	app.Post("/clientes/:id/transacoes", postTransacao(db))

	app.Listen(":8080")
}

func getExtrato(db *pgxpool.Pool) func(*fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		const (
			pegaSaldo      = `SELECT client_limit, balance, now() FROM clients WHERE id = $1`
			pegaTransacoes = `SELECT transaction_value, transaction_type, transaction_description, transaction_date FROM transactions WHERE client_id = $1 ORDER BY id DESC LIMIT 10`
		)

		type saldo struct {
			Limite      int       `json:"limite"`
			Total       int       `json:"total"`
			DataExtrato time.Time `json:"data_extrato"`
		}

		type transacao struct {
			Valor       int       `json:"valor"`
			Tipo        string    `json:"tipo"`
			Descricao   string    `json:"descricao"`
			RealizadaEm time.Time `json:"realizada_em"`
		}

		ctx := c.Context()
		id := c.Params("id")
		rows, err := db.Query(ctx, pegaSaldo, id)
		if err != nil {
			return err
		}

		balanceDetails, err := pgx.CollectOneRow(rows, pgx.RowToStructByPos[saldo])
		if err != nil {
			if err.Error() == pgx.ErrNoRows.Error() {
				return c.SendStatus(404)
			}

			return c.SendStatus(500)
		}

		rows, err = db.Query(ctx, pegaTransacoes, id)
		if err != nil {
			return err
		}

		transactions, err := pgx.CollectRows(rows, pgx.RowToStructByPos[transacao])
		if err != nil {
			return c.SendStatus(500)
		}

		return c.Status(200).JSON(struct {
			Saldo             saldo       `json:"saldo"`
			UltimasTransacoes []transacao `json:"ultimas_transacoes"`
		}{
			Saldo:             balanceDetails,
			UltimasTransacoes: transactions,
		})
	}
}

func postTransacao(db *pgxpool.Pool) func(*fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		const (
			pegaSaldoAtualizar = `SELECT client_limit, balance FROM clients WHERE id=$1 FOR UPDATE`
			insereTransacao    = `INSERT INTO transactions(client_id, transaction_value, transaction_type, transaction_description, transaction_date) VALUES ($1, $2, $3, $4, now())`
			atualizaSaldo      = `UPDATE clients SET balance = $1 WHERE id = $2`
		)

		type reqTransacao struct {
			Valor     int    `json:"valor"`
			Tipo      string `json:"tipo"`
			Descricao string `json:"descricao"`
		}

		var transacao reqTransacao
		if err := c.BodyParser(&transacao); err != nil {
			return c.SendStatus(422)
		}

		tam := len(transacao.Descricao)
		if transacao.Valor == 0 || (transacao.Tipo != "c" && transacao.Tipo != "d") || tam == 0 || tam > 10 {
			return c.SendStatus(422)
		}

		ctx := c.Context()
		tx, err := db.Begin(ctx)
		if err != nil {
			return c.SendStatus(422)
		}
		defer tx.Rollback(ctx)

		var limite, saldo int
		id := c.Params("id")
		if err = tx.QueryRow(ctx, pegaSaldoAtualizar, id).Scan(&limite, &saldo); err != nil {
			if err.Error() == pgx.ErrNoRows.Error() {
				return c.SendStatus(404)
			}

			return c.SendStatus(422)
		}

		if transacao.Tipo == "c" {
			saldo += transacao.Valor
		} else {
			saldo -= transacao.Valor
			if (limite + saldo) < 0 {
				return c.SendStatus(422)
			}
		}

		batch := pgx.Batch{}
		batch.Queue(insereTransacao, id, transacao.Valor, transacao.Tipo, transacao.Descricao)
		batch.Queue(atualizaSaldo, saldo, id)

		batchResults := tx.SendBatch(ctx, &batch)
		if _, err = batchResults.Exec(); err != nil {
			return c.SendStatus(422)
		}

		if err = batchResults.Close(); err != nil {
			return c.SendStatus(422)
		}

		err = tx.Commit(ctx)
		if err != nil {
			return c.SendStatus(422)
		}

		return c.Status(200).JSON(struct {
			Limite int `json:"limite"`
			Saldo  int `json:"saldo"`
		}{
			Saldo:  saldo,
			Limite: limite,
		})
	}
}
