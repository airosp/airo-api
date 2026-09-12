package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type Challenge struct {
	ID          string
	PhoneE164   string
	CodeHash    []byte
	Channel     string
	Status      string
	Attempts    int
	MaxAttempts int
	DeviceID    *string

	// CreatedAt vem do `Clock`, não do `now()` da base de dados.
	//
	// O `CHECK` do esquema compara `expires_at > created_at`. Com o `created_at`
	// a vir do `DEFAULT now()` e a expiração calculada na aplicação, as duas
	// datas vêm de relógios diferentes — e basta um desvio de segundos entre a
	// API e o Postgres para o `INSERT` falhar **depois** de o código já ter sido
	// enviado. A pessoa pagaria a mensagem para receber um erro.
	CreatedAt time.Time
	ExpiresAt time.Time
}

var ErrChallengeNotFound = errors.New("desafio não encontrado")

type AuthRepo struct{ tx *TxManager }

func NewAuthRepo(tx *TxManager) *AuthRepo { return &AuthRepo{tx: tx} }

// CreateChallenge abre um desafio e invalida o anterior.
//
// Um pendente por número: dois códigos válidos ao mesmo tempo duplicam as
// tentativas, e o índice único do esquema garante-o mesmo com dois pedidos em
// paralelo.
func (r *AuthRepo) CreateChallenge(ctx context.Context, c Challenge) (string, error) {
	var id string
	err := r.tx.Do(ctx, func(ctx context.Context) error {
		q := r.tx.Q(ctx)
		if _, err := q.Exec(ctx,
			`UPDATE otp_challenge SET status = 'superseded'
			  WHERE phone_e164 = $1 AND status = 'pending'`, c.PhoneE164); err != nil {
			return fmt.Errorf("invalidar desafio anterior: %w", err)
		}
		return q.QueryRow(ctx,
			`INSERT INTO otp_challenge (phone_e164, code_hash, channel, device_id, created_at, expires_at, max_attempts)
			 VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
			c.PhoneE164, c.CodeHash, c.Channel, c.DeviceID, c.CreatedAt, c.ExpiresAt, c.MaxAttempts,
		).Scan(&id)
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

func (r *AuthRepo) Challenge(ctx context.Context, id string) (Challenge, error) {
	var c Challenge
	err := r.tx.Q(ctx).QueryRow(ctx,
		`SELECT id, phone_e164, code_hash, channel, status, attempts, max_attempts, device_id, expires_at
		   FROM otp_challenge WHERE id = $1`, id,
	).Scan(&c.ID, &c.PhoneE164, &c.CodeHash, &c.Channel, &c.Status, &c.Attempts, &c.MaxAttempts, &c.DeviceID, &c.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrChallengeNotFound
	}
	return c, err
}

// CountAttempt regista a tentativa e devolve quantas já vão. Fecha o desafio ao
// esgotar — reduzindo 10⁶ possibilidades efectivas a cinco.
func (r *AuthRepo) CountAttempt(ctx context.Context, id string) (int, error) {
	var attempts int
	err := r.tx.Q(ctx).QueryRow(ctx,
		`UPDATE otp_challenge
		    SET attempts = attempts + 1,
		        status = CASE WHEN attempts + 1 >= max_attempts THEN 'exhausted'::otp_status ELSE status END
		  WHERE id = $1 RETURNING attempts`, id).Scan(&attempts)
	return attempts, err
}

func (r *AuthRepo) MarkVerified(ctx context.Context, id string) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`UPDATE otp_challenge SET status = 'verified', verified_at = now() WHERE id = $1`, id)
	return err
}

// UserByPhone devolve o utilizador, se existir.
func (r *AuthRepo) UserByPhone(ctx context.Context, phone string) (string, bool, error) {
	var id string
	err := r.tx.Q(ctx).QueryRow(ctx,
		`SELECT id FROM app_user WHERE phone_e164 = $1 AND deleted_at IS NULL`, phone).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return id, err == nil, err
}

// CreateUser cria a conta. Chamado **no verify**, nunca no request.
func (r *AuthRepo) CreateUser(ctx context.Context, phone, region string) (string, error) {
	var id string
	err := r.tx.Q(ctx).QueryRow(ctx,
		`INSERT INTO app_user (phone_e164, phone_region) VALUES ($1,$2)
		 ON CONFLICT (phone_e164) DO UPDATE SET last_seen_at = now()
		 RETURNING id`, phone, region).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("criar utilizador: %w", err)
	}
	return id, nil
}

func (r *AuthRepo) TouchDevice(ctx context.Context, deviceID, userID, platform string) error {
	if deviceID == "" {
		return nil
	}
	_, err := r.tx.Q(ctx).Exec(ctx,
		`INSERT INTO device (id, user_id, platform) VALUES ($1,$2,$3)
		 ON CONFLICT (id) DO UPDATE SET last_seen_at = now(), user_id = EXCLUDED.user_id`,
		deviceID, userID, platform)
	return err
}

// ── Refresh tokens ───────────────────────────────────────────────────────────

type RefreshToken struct {
	ID        string
	UserID    string
	DeviceID  string
	FamilyID  string
	ExpiresAt time.Time
	RevokedAt *time.Time
}

var (
	ErrTokenNotFound = errors.New("token não encontrado")
	// ErrTokenReuse é o caso grave: um token já usado voltou a aparecer.
	ErrTokenReuse = errors.New("reutilização de token detectada")
)

// InsertRefresh grava o token. Com `familyID` vazio abre uma família nova — e
// quem a gera é a base de dados: formatar um uuid à mão produz uuids inválidos
// que só falham no INSERT.
func (r *AuthRepo) InsertRefresh(ctx context.Context, userID, deviceID, familyID string, hash []byte, expiresAt time.Time) (id, family string, err error) {
	if familyID == "" {
		err = r.tx.Q(ctx).QueryRow(ctx,
			`INSERT INTO refresh_token (user_id, device_id, family_id, token_hash, expires_at)
			 VALUES ($1,$2,gen_random_uuid(),$3,$4) RETURNING id, family_id`,
			userID, deviceID, hash, expiresAt).Scan(&id, &family)
		return id, family, err
	}
	err = r.tx.Q(ctx).QueryRow(ctx,
		`INSERT INTO refresh_token (user_id, device_id, family_id, token_hash, expires_at)
		 VALUES ($1,$2,$3,$4,$5) RETURNING id, family_id`,
		userID, deviceID, familyID, hash, expiresAt).Scan(&id, &family)
	return id, family, err
}

func (r *AuthRepo) RefreshByHash(ctx context.Context, hash []byte) (RefreshToken, error) {
	var t RefreshToken
	err := r.tx.Q(ctx).QueryRow(ctx,
		`SELECT id, user_id, device_id, family_id, expires_at, revoked_at
		   FROM refresh_token WHERE token_hash = $1`, hash,
	).Scan(&t.ID, &t.UserID, &t.DeviceID, &t.FamilyID, &t.ExpiresAt, &t.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrTokenNotFound
	}
	return t, err
}

// RevokeFamily derruba a família inteira.
//
// Se um token já usado voltar a aparecer, **foi roubado**: não se sabe qual das
// duas partes é a legítima, e deixar as duas a correr é deixar o atacante lá
// dentro. A sessão termina em todos os aparelhos, e a pessoa volta a entrar.
func (r *AuthRepo) RevokeFamily(ctx context.Context, familyID, reason string) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`UPDATE refresh_token SET revoked_at = now(), revoke_reason = $2
		  WHERE family_id = $1 AND revoked_at IS NULL`, familyID, reason)
	return err
}

func (r *AuthRepo) ReplaceRefresh(ctx context.Context, oldID, newID string) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`UPDATE refresh_token SET revoked_at = now(), revoke_reason = 'rotated', replaced_by = $2
		  WHERE id = $1`, oldID, newID)
	return err
}

// AppendAuthEvent escreve no trilho de auditoria. **Sem o código.**
func (r *AuthRepo) AppendAuthEvent(ctx context.Context, kind string, userID, phone, deviceID *string, meta map[string]any) error {
	blob := []byte("{}")
	if meta != nil {
		b, err := json.Marshal(meta)
		if err != nil {
			return err
		}
		blob = b
	}
	_, err := r.tx.Q(ctx).Exec(ctx,
		`INSERT INTO auth_event (kind, user_id, phone_e164, device_id, meta) VALUES ($1,$2,$3,$4,$5)`,
		kind, userID, phone, deviceID, blob)
	return err
}

// WithTx expõe o limite de transacção ao serviço.
func (r *AuthRepo) WithTx(ctx context.Context, fn func(context.Context) error) error {
	return r.tx.Do(ctx, fn)
}
