package service

import (
	"context"
	"errors"

	repo "github.com/airosp/airo-api/internal/repository/postgres"
)

// AccountStore apaga a conta e devolve o número, para se saber o que anonimizar.
type AccountStore interface {
	PhoneOf(ctx context.Context, userID string) (string, error)
	Delete(ctx context.Context, userID, phone string) (int64, error)
}

// ImageDeleter apaga as imagens da pessoa no serviço de imagens.
type ImageDeleter interface {
	DeleteAvatar(ctx context.Context, publicID string) error
}

type AccountService struct {
	accounts AccountStore
	images   ImageDeleter
}

func NewAccountService(a AccountStore, images ImageDeleter) *AccountService {
	return &AccountService{accounts: a, images: images}
}

// DeleteResult conta o que aconteceu, sem dizer a quem.
type DeleteResult struct {
	AuditAnonymised int64
	PhotoRemoved    bool
}

// Delete apaga a conta inteira.
//
// ⚠️ **Irreversível e sem período de graça.** Um "apagar" que guarda trinta
// dias é uma promessa que não se cumpre — e quem pede para ser esquecido não
// está a pedir para ser arquivado. Se for preciso um período de graça, é uma
// decisão de produto com ecrã próprio, não um comportamento escondido aqui.
//
// A fotografia sai primeiro. Ao contrário das linhas da base de dados, ela vive
// noutro serviço e não cascateia: apagar a conta e deixar a cara na Cloudinary
// seria apagar o índice e guardar o livro.
func (s *AccountService) Delete(ctx context.Context, userID string) (DeleteResult, error) {
	var out DeleteResult

	phone, err := s.accounts.PhoneOf(ctx, userID)
	if err != nil {
		return out, err
	}

	if s.images != nil {
		// Uma imagem que já lá não está não é erro: quem pede para apagar quer
		// que deixe de existir, e já não existe.
		if err := s.images.DeleteAvatar(ctx, userID); err != nil && !errors.Is(err, ErrNoUploader) {
			// Falhar aqui **não** trava a eliminação. Uma conta que não se
			// consegue apagar porque um serviço de imagens está em baixo é uma
			// conta que a pessoa não consegue apagar — e isso é pior do que uma
			// imagem órfã, que se limpa depois.
			out.PhotoRemoved = false
		} else {
			out.PhotoRemoved = true
		}
	}

	anonimizadas, err := s.accounts.Delete(ctx, userID, phone)
	if err != nil {
		return out, err
	}
	out.AuditAnonymised = anonimizadas
	return out, nil
}

var _ = repo.ErrNotFound
