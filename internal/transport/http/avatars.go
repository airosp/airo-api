package http

import (
	"context"

	"github.com/airosp/airo-api/internal/platform/cloudinary"
)

// avatarUploader liga o serviço à Cloudinary.
//
// Vive aqui, no sítio onde as peças se montam, para o serviço não importar a
// plataforma nem a plataforma conhecer o serviço.
type avatarUploader struct{ c *cloudinary.Client }

func (u avatarUploader) UploadAvatar(
	ctx context.Context, image []byte, publicID string,
) (string, string, bool, error) {
	up, err := u.c.Upload(ctx, image, publicID)
	if err != nil {
		return "", "", false, err
	}
	full, thumb := u.c.AvatarURLs(up)
	return full, thumb, up.FaceFound(), nil
}
