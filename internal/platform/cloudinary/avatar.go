package cloudinary

// O enquadramento de um avatar.
//
// Um avatar é um círculo pequeno. Cortar uma fotografia ao meio deixa lá o que
// estiver no centro do rectângulo — que numa fotografia de corpo inteiro é a
// barriga, e num retrato tirado de lado é uma orelha.
//
//	c_fill    corta e preenche o quadrado, sem deformar
//	g_face    o corte segue a cara
//	z_0.7     afasta: uma cara colada às bordas parece uma fotografia de passe
//	q_auto    qualidade decidida pela imagem, não por nós
//	f_auto    formato decidido pelo browser (WebP, AVIF) — metade dos bytes
const (
	// comFace é usado quando a Cloudinary encontrou uma cara.
	comFace = "c_fill,g_face,z_0.7,q_auto,f_auto"
	// semFace é o caminho honesto quando não encontrou nenhuma.
	//
	// `g_auto` centra no que a imagem tem de mais saliente. Insistir em
	// `g_face` sem cara faz a Cloudinary cair no centro geométrico — e o centro
	// de uma fotografia de um haltere é o chão.
	semFace = "c_fill,g_auto,q_auto,f_auto"
)

// O enquadramento de um prato.
//
// Não é o de uma cara, e a diferença importa: `g_face` numa fotografia de
// comida não encontra nada e cai no centro geométrico — que num prato
// fotografado de cima é a mesa.
//
//	c_fill   corta e preenche, sem deformar
//	g_auto   o corte segue o que a imagem tem de mais saliente, que num prato
//	         fotografado é a comida
//
// Sem `z_`: afastar um retrato evita a fotografia de passe, afastar um prato só
// mostra mais mesa.
const daComida = "c_fill,g_auto,q_auto,f_auto"

// MealURLs devolve os dois tamanhos de uma fotografia de refeição.
//
// Maior do que um avatar: uma refeição olha-se, e é a prova do que se comeu —
// 1024 para a ver, 256 para a lista de registos recentes.
func (c *Client) MealURLs(u Uploaded) (full, thumb string) {
	return c.URL(u, daComida+",w_1024,h_1024"), c.URL(u, daComida+",w_256,h_256")
}

// AvatarURLs devolve os dois tamanhos que a interface desenha.
//
// Dois e não um: o ecrã de perfil mostra o avatar grande e a lista de
// especialistas mostra-o pequeno. Pedir o de 512 para desenhar a 32 gasta
// dezasseis vezes mais bytes na ligação de quem está no ginásio.
func (c *Client) AvatarURLs(u Uploaded) (full, thumb string) {
	base := semFace
	if u.FaceFound() {
		base = comFace
	}
	return c.URL(u, base+",w_512,h_512"), c.URL(u, base+",w_128,h_128")
}
