package pexels

import "testing"

/*
 * O ficheiro escolhido é o maior que ainda cabe num telemóvel.
 *
 * O acervo devolve o mesmo vídeo em seis tamanhos, até 4K. Mandar 4K para um
 * ecrã de telemóvel é banda gasta em pixels que ele não tem — e numa rede de
 * ginásio é o vídeo a não começar.
 */
func TestEscolheOMaiorQueAindaCabe(t *testing.T) {
	f, ok := melhorFicheiro([]apiVideoFile{
		{Link: "sd", FileType: "video/mp4", Width: 640, Height: 360},
		{Link: "hd", FileType: "video/mp4", Width: 1280, Height: 720},
		{Link: "uhd", FileType: "video/mp4", Width: 3840, Height: 2160},
	})
	if !ok || f.Link != "hd" {
		t.Fatalf("escolheu %+v, esperava o de 1280", f)
	}
}

// Se só houver tamanhos grandes, vai o mais pequeno deles — melhor um vídeo
// pesado do que nenhum.
func TestSeTodosPassaremVaiOMaisPequeno(t *testing.T) {
	f, ok := melhorFicheiro([]apiVideoFile{
		{Link: "uhd", FileType: "video/mp4", Width: 3840},
		{Link: "qhd", FileType: "video/mp4", Width: 2560},
	})
	if !ok || f.Link != "qhd" {
		t.Fatalf("escolheu %+v, esperava o de 2560", f)
	}
}

// O que não é mp4 não serve: o leitor não o abre.
func TestIgnoraOQueNaoEMp4(t *testing.T) {
	if _, ok := melhorFicheiro([]apiVideoFile{
		{Link: "webm", FileType: "video/webm", Width: 1280},
	}); ok {
		t.Error("aceitou um ficheiro que o leitor não abre")
	}
}

// Sem chave, o cliente diz que não está configurado em vez de tentar e falhar.
func TestSemChaveNaoEstaConfigurado(t *testing.T) {
	if New("").Configured() {
		t.Error("um cliente sem chave deu-se por configurado")
	}
	if !New("abc").Configured() {
		t.Error("um cliente com chave deu-se por não configurado")
	}
}
