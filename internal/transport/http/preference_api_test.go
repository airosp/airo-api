package http_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// As preferências de exercício sobrevivem à ida e volta.
//
// Viviam só no telemóvel, e a consequência aparecia no Modo Foco: o servidor
// montava a sessão sem elas, o pacote descrevia outro treino e o ecrã voltava
// ao motor local. Quem personalizava o plano deixava de receber o que o
// servidor decide.
func TestPreferenciasDeExercicioVaoEVoltam(t *testing.T) {
	h, _ := serveProfileCom(t, nil)

	corpo := `{"displayName":"Ana","age":30,"sex":"female","heightCm":165,"weightKg":62,
	  "experience":"intermediate","workoutDays":[0,2,4],"workoutMinutes":45,"workoutTime":"morning",
	  "equipment":["dumbbells"],"dietStyle":"omnivore","mealsPerDay":4,"foodBudget":"medium",
	  "foodExclusions":[],
	  "pinnedExercises":["push_up","squat_bw"],
	  "excludedExercises":["burpee"],
	  "exercisePrescriptions":{"push_up":{"sets":4,"target":12}}}`

	if w := put(t, h, "/v1/profile", corpo); w.Code != http.StatusOK {
		t.Fatalf("gravar: %d — %s", w.Code, w.Body.String())
	}

	var got struct {
		Pinned   []string `json:"pinnedExercises"`
		Excluded []string `json:"excludedExercises"`
		Pres     map[string]struct {
			Sets   int `json:"sets"`
			Target int `json:"target"`
		} `json:"exercisePrescriptions"`
	}
	w := get(t, h, "/v1/profile")
	if w.Code != http.StatusOK {
		t.Fatalf("ler: %d", w.Code)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}

	if len(got.Pinned) != 2 || got.Pinned[0] != "push_up" || got.Pinned[1] != "squat_bw" {
		t.Errorf("fixados: %v", got.Pinned)
	}
	if len(got.Excluded) != 1 || got.Excluded[0] != "burpee" {
		t.Errorf("excluídos: %v", got.Excluded)
	}
	if p := got.Pres["push_up"]; p.Sets != 4 || p.Target != 12 {
		t.Errorf("prescrição: %+v", p)
	}
}

// Ausente é "não mexer"; vazio é "limpa".
//
// Sem esta distinção, um cliente antigo que não envie as preferências apagava
// as escolhas de quem ainda não tinha actualizado a app — no primeiro `PUT` que
// a app fizesse, que é a cada edição de perfil.
func TestPreferenciasAusentesNaoApagam(t *testing.T) {
	h, _ := serveProfileCom(t, nil)

	comPrefs := `{"displayName":"Ana","age":30,"sex":"female","heightCm":165,"weightKg":62,
	  "experience":"intermediate","workoutDays":[0,2,4],"workoutMinutes":45,"workoutTime":"morning",
	  "equipment":["dumbbells"],"dietStyle":"omnivore","mealsPerDay":4,"foodBudget":"medium",
	  "foodExclusions":[],"pinnedExercises":["push_up"]}`
	if w := put(t, h, "/v1/profile", comPrefs); w.Code != http.StatusOK {
		t.Fatalf("gravar: %d", w.Code)
	}

	semPrefs := `{"displayName":"Ana","age":31,"sex":"female","heightCm":165,"weightKg":62,
	  "experience":"intermediate","workoutDays":[0,2,4],"workoutMinutes":45,"workoutTime":"morning",
	  "equipment":["dumbbells"],"dietStyle":"omnivore","mealsPerDay":4,"foodBudget":"medium",
	  "foodExclusions":[]}`
	if w := put(t, h, "/v1/profile", semPrefs); w.Code != http.StatusOK {
		t.Fatalf("regravar: %d", w.Code)
	}

	var got struct {
		Pinned []string `json:"pinnedExercises"`
	}
	json.Unmarshal(get(t, h, "/v1/profile").Body.Bytes(), &got)
	if len(got.Pinned) != 1 {
		t.Errorf("um PUT sem preferências apagou-as: %v", got.Pinned)
	}

	vazio := `{"displayName":"Ana","age":31,"sex":"female","heightCm":165,"weightKg":62,
	  "experience":"intermediate","workoutDays":[0,2,4],"workoutMinutes":45,"workoutTime":"morning",
	  "equipment":["dumbbells"],"dietStyle":"omnivore","mealsPerDay":4,"foodBudget":"medium",
	  "foodExclusions":[],"pinnedExercises":[]}`
	if w := put(t, h, "/v1/profile", vazio); w.Code != http.StatusOK {
		t.Fatalf("limpar: %d", w.Code)
	}
	json.Unmarshal(get(t, h, "/v1/profile").Body.Bytes(), &got)
	if len(got.Pinned) != 0 {
		t.Errorf("uma lista vazia devia limpar: %v", got.Pinned)
	}
}

// Excluir ganha a fixar. São contraditórios, e resolver no serviço evita que o
// resultado dependa da ordem de leitura — o que daria treinos diferentes em
// dias diferentes.
func TestExcluirGanhaAFixar(t *testing.T) {
	h, _ := serveProfileCom(t, nil)
	corpo := `{"displayName":"Ana","age":30,"sex":"female","heightCm":165,"weightKg":62,
	  "experience":"intermediate","workoutDays":[0,2,4],"workoutMinutes":45,"workoutTime":"morning",
	  "equipment":["dumbbells"],"dietStyle":"omnivore","mealsPerDay":4,"foodBudget":"medium",
	  "foodExclusions":[],"pinnedExercises":["burpee"],"excludedExercises":["burpee"]}`
	if w := put(t, h, "/v1/profile", corpo); w.Code != http.StatusOK {
		t.Fatalf("gravar: %d — %s", w.Code, w.Body.String())
	}

	var got struct {
		Pinned   []string `json:"pinnedExercises"`
		Excluded []string `json:"excludedExercises"`
	}
	json.Unmarshal(get(t, h, "/v1/profile").Body.Bytes(), &got)
	if len(got.Pinned) != 0 {
		t.Errorf("ficou fixado e excluído ao mesmo tempo: %v", got.Pinned)
	}
	if len(got.Excluded) != 1 {
		t.Errorf("excluídos: %v", got.Excluded)
	}
}
