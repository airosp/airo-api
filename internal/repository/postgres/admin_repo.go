package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// AdminRepo guarda a equipa: membros, entradas, sessões e a auditoria.
type AdminRepo struct{ tx *TxManager }

func NewAdminRepo(tx *TxManager) *AdminRepo { return &AdminRepo{tx: tx} }

// Tx expõe o limite das transacções a quem orquestra várias escritas.
func (r *AdminRepo) Tx() *TxManager { return r.tx }

// ErrDuplicate é uma chave única que já existe.
var ErrDuplicate = errors.New("já existe")

func uniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

type MemberRow struct {
	ID             string
	Email          string
	Name           string
	Role           string
	SpecialistID   *string
	PasswordHash   string
	TOTPSecret     []byte
	TOTPEnabled    bool
	Active         bool
	FailedAttempts int
	LockedUntil    *time.Time
	InviteExpires  *time.Time
	CreatedAt      time.Time
	LastSeenAt     *time.Time
}

// Sempre com o alias `m`, para servir também nas junções.
const memberCols = `m.id, m.email::text, m.name, m.role::text, m.specialist_id, coalesce(m.password_hash, ''),
	m.totp_secret, m.totp_enabled, m.active, m.failed_attempts, m.locked_until, m.invite_expires,
	m.created_at, m.last_seen_at`

func scanMember(row pgx.Row) (MemberRow, error) {
	var m MemberRow
	err := row.Scan(&m.ID, &m.Email, &m.Name, &m.Role, &m.SpecialistID, &m.PasswordHash,
		&m.TOTPSecret, &m.TOTPEnabled, &m.Active, &m.FailedAttempts, &m.LockedUntil,
		&m.InviteExpires, &m.CreatedAt, &m.LastSeenAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

func (r *AdminRepo) MemberByEmail(ctx context.Context, email string) (MemberRow, error) {
	return scanMember(r.tx.Q(ctx).QueryRow(ctx,
		`SELECT `+memberCols+` FROM admin_member m WHERE email = $1`, email))
}

func (r *AdminRepo) MemberByID(ctx context.Context, id string) (MemberRow, error) {
	return scanMember(r.tx.Q(ctx).QueryRow(ctx,
		`SELECT `+memberCols+` FROM admin_member m WHERE id = $1`, id))
}

func (r *AdminRepo) MemberByInvite(ctx context.Context, hash []byte, now time.Time) (MemberRow, error) {
	return scanMember(r.tx.Q(ctx).QueryRow(ctx,
		`SELECT `+memberCols+` FROM admin_member m
		  WHERE invite_hash = $1 AND invite_expires > $2 AND active`, hash, now))
}

func (r *AdminRepo) Members(ctx context.Context) ([]MemberRow, error) {
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT `+memberCols+` FROM admin_member m ORDER BY m.active DESC, m.created_at`)
	if err != nil {
		return nil, fmt.Errorf("ler equipa: %w", err)
	}
	defer rows.Close()
	out := []MemberRow{}
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *AdminRepo) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := r.tx.Q(ctx).QueryRow(ctx,
		`SELECT count(*) FROM admin_member m WHERE role = 'admin' AND active`).Scan(&n)
	return n, err
}

// Invite cria o membro, ou repõe o convite de um que já exista com o mesmo
// email. Repor apaga a palavra-passe e a app autenticadora: é o caminho para
// quem perdeu o telemóvel.
func (r *AdminRepo) Invite(ctx context.Context, email, name, role string, specialistID *string,
	inviteHash []byte, expires time.Time) (MemberRow, error) {
	return scanMember(r.tx.Q(ctx).QueryRow(ctx,
		`INSERT INTO admin_member AS m (email, name, role, specialist_id, invite_hash, invite_expires)
		 VALUES ($1, $2, $3::admin_role, $4, $5, $6)
		 ON CONFLICT (email) DO UPDATE SET
		   name = EXCLUDED.name, role = EXCLUDED.role, specialist_id = EXCLUDED.specialist_id,
		   invite_hash = EXCLUDED.invite_hash, invite_expires = EXCLUDED.invite_expires,
		   password_hash = NULL, totp_secret = NULL, totp_enabled = false,
		   failed_attempts = 0, locked_until = NULL, active = true
		 RETURNING `+memberCols,
		email, name, role, specialistID, inviteHash, expires))
}

// ResetInvite volta a pôr o membro por activar, com um link novo.
func (r *AdminRepo) ResetInvite(ctx context.Context, id string, inviteHash []byte, expires time.Time) error {
	return r.tx.Do(ctx, func(ctx context.Context) error {
		tag, err := r.tx.Q(ctx).Exec(ctx,
			`UPDATE admin_member SET invite_hash = $2, invite_expires = $3, password_hash = NULL,
			        totp_secret = NULL, totp_enabled = false, failed_attempts = 0, locked_until = NULL
			  WHERE id = $1`, id, inviteHash, expires)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		_, err = r.tx.Q(ctx).Exec(ctx,
			`UPDATE admin_session SET revoked_at = now() WHERE member_id = $1 AND revoked_at IS NULL`, id)
		return err
	})
}

// SetPendingTOTP guarda o segredo que o convite mostra, antes de confirmado.
func (r *AdminRepo) SetPendingTOTP(ctx context.Context, id string, sealed []byte) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`UPDATE admin_member SET totp_secret = $2 WHERE id = $1 AND NOT totp_enabled`, id, sealed)
	return err
}

// Activate fecha o convite: palavra-passe e app autenticadora confirmadas.
func (r *AdminRepo) Activate(ctx context.Context, id, name, passwordHash string) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`UPDATE admin_member SET name = $2, password_hash = $3, totp_enabled = true,
		        invite_hash = NULL, invite_expires = NULL, failed_attempts = 0, locked_until = NULL
		  WHERE id = $1`, id, name, passwordHash)
	return err
}

// LoginFailed conta uma falha e tranca à quinta.
func (r *AdminRepo) LoginFailed(ctx context.Context, id string, lockFor time.Duration, now time.Time) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`UPDATE admin_member SET
		   failed_attempts = CASE WHEN failed_attempts + 1 >= 5 THEN 0 ELSE failed_attempts + 1 END,
		   locked_until = CASE WHEN failed_attempts + 1 >= 5 THEN $2::timestamptz ELSE locked_until END
		 WHERE id = $1`, id, now.Add(lockFor))
	return err
}

func (r *AdminRepo) LoginOK(ctx context.Context, id string, now time.Time) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`UPDATE admin_member SET failed_attempts = 0, locked_until = NULL, last_seen_at = $2 WHERE id = $1`,
		id, now)
	return err
}

func (r *AdminRepo) UpdateMember(ctx context.Context, id string, role *string, active *bool) (MemberRow, error) {
	m, err := scanMember(r.tx.Q(ctx).QueryRow(ctx,
		`UPDATE admin_member m SET
		   role = COALESCE($2::admin_role, role),
		   active = COALESCE($3, active)
		 WHERE id = $1 RETURNING `+memberCols, id, role, active))
	if err != nil {
		return m, err
	}
	if !m.Active {
		_, err = r.tx.Q(ctx).Exec(ctx,
			`UPDATE admin_session SET revoked_at = now() WHERE member_id = $1 AND revoked_at IS NULL`, id)
	}
	return m, err
}

// ── Os dois passos ───────────────────────────────────────────────────────────

func (r *AdminRepo) NewChallenge(ctx context.Context, hash []byte, memberID string, expires time.Time) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`INSERT INTO admin_challenge (hash, member_id, expires_at) VALUES ($1, $2, $3)`,
		hash, memberID, expires)
	return err
}

// Challenge conta a tentativa ao ler: cinco códigos errados gastam o desafio.
func (r *AdminRepo) Challenge(ctx context.Context, hash []byte, now time.Time) (memberID string, attempts int, err error) {
	err = r.tx.Q(ctx).QueryRow(ctx,
		`UPDATE admin_challenge SET attempts = attempts + 1
		  WHERE hash = $1 AND expires_at > $2 RETURNING member_id, attempts`, hash, now).
		Scan(&memberID, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, ErrNotFound
	}
	return memberID, attempts, err
}

func (r *AdminRepo) DropChallenge(ctx context.Context, hash []byte) error {
	_, err := r.tx.Q(ctx).Exec(ctx, `DELETE FROM admin_challenge WHERE hash = $1 OR expires_at < now()`, hash)
	return err
}

// ── Sessões ──────────────────────────────────────────────────────────────────

func (r *AdminRepo) NewSession(ctx context.Context, hash []byte, memberID string, expires time.Time) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`INSERT INTO admin_session (hash, member_id, expires_at) VALUES ($1, $2, $3)`,
		hash, memberID, expires)
	return err
}

// SessionMember devolve o membro de uma sessão válida. Um membro desactivado
// perde as sessões no mesmo instante, mesmo que alguma tenha escapado.
func (r *AdminRepo) SessionMember(ctx context.Context, hash []byte, now time.Time) (MemberRow, time.Time, error) {
	var expires time.Time
	var m MemberRow
	err := r.tx.Q(ctx).QueryRow(ctx,
		`SELECT s.expires_at, `+memberCols+`
		   FROM admin_session s JOIN admin_member m ON m.id = s.member_id
		  WHERE s.hash = $1 AND s.revoked_at IS NULL AND s.expires_at > $2 AND m.active AND m.totp_enabled`,
		hash, now).Scan(&expires, &m.ID, &m.Email, &m.Name, &m.Role, &m.SpecialistID, &m.PasswordHash,
		&m.TOTPSecret, &m.TOTPEnabled, &m.Active, &m.FailedAttempts, &m.LockedUntil,
		&m.InviteExpires, &m.CreatedAt, &m.LastSeenAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, expires, ErrNotFound
	}
	return m, expires, err
}

func (r *AdminRepo) RevokeSession(ctx context.Context, hash []byte) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`UPDATE admin_session SET revoked_at = now() WHERE hash = $1 AND revoked_at IS NULL`, hash)
	return err
}

func (r *AdminRepo) TouchMember(ctx context.Context, id string, now time.Time) {
	_, _ = r.tx.Q(ctx).Exec(ctx,
		`UPDATE admin_member SET last_seen_at = $2
		  WHERE id = $1 AND (last_seen_at IS NULL OR last_seen_at < $2::timestamptz - interval '1 minute')`, id, now)
}

// ── Auditoria ────────────────────────────────────────────────────────────────

type AuditRow struct {
	ID        int64
	At        time.Time
	MemberID  *string
	ActorName *string
	Action    string
	Target    string
	Detail    string
}

func (r *AdminRepo) Audit(ctx context.Context, memberID *string, actor, action, target, detail string) error {
	_, err := r.tx.Q(ctx).Exec(ctx,
		`INSERT INTO admin_audit (member_id, actor_name, action, target, detail) VALUES ($1, NULLIF($2,''), $3, $4, $5)`,
		memberID, actor, action, target, detail)
	if err != nil {
		return fmt.Errorf("gravar auditoria: %w", err)
	}
	return nil
}

func (r *AdminRepo) AuditLog(ctx context.Context, limit int, before int64) ([]AuditRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.tx.Q(ctx).Query(ctx,
		`SELECT id, at, member_id::text, actor_name, action, target, detail FROM admin_audit
		  WHERE ($2 = 0 OR id < $2) ORDER BY id DESC LIMIT $1`, limit, before)
	if err != nil {
		return nil, fmt.Errorf("ler auditoria: %w", err)
	}
	defer rows.Close()
	out := []AuditRow{}
	for rows.Next() {
		var a AuditRow
		if err := rows.Scan(&a.ID, &a.At, &a.MemberID, &a.ActorName, &a.Action, &a.Target, &a.Detail); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func isFKViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23503"
}
