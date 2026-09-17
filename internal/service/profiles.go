package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/airosp/airo-api/internal/engine/nutrition"
	"github.com/airosp/airo-api/internal/engine/training"
	repo "github.com/airosp/airo-api/internal/repository/postgres"
)

// ErrProfileMissing e ErrWeightMissing atravessam a fronteira para o handler
// poder responder coisas diferentes: "ainda não criaste o perfil" e "falta
// dizer-nos o teu peso" não são o mesmo pedido a fazer a alguém.
var (
	ErrProfileMissing = repo.ErrNoProfile
	ErrWeightMissing  = repo.ErrNoWeight
)

// Profiles traduz o perfil gravado no que os motores precisam.
//
// Vive aqui e não no repositório porque é o serviço que conhece os dois lados —
// e porque a tradução é uma decisão: sem plano ainda, o dia é de corpo inteiro,
// que é melhor do que não haver treino nenhum para mostrar.
type Profiles struct {
	prefs *repo.PreferenceRepo
	/*
	 * equipa diz quem a pessoa pôs como treinador — o único papel que muda o
	 * treino. Nulo deixa o plano como o motor o monta, que é o que acontece em
	 * qualquer arnês de teste que não a ligue.
	 */
	equipa   *repo.SpecialistRepo
	repo     *repo.ProfileRepo
	uploader Uploader
	// NutritionGoal por omissão até o objectivo existir.
	defaultNutritionGoal string
}

func NewProfiles(r *repo.ProfileRepo, up Uploader, prefs *repo.PreferenceRepo) *Profiles {
	return &Profiles{repo: r, uploader: up, prefs: prefs, defaultNutritionGoal: "maintain"}
}

// SaveProfileInput é o que a app manda no fim do onboarding.
type SaveProfileInput struct {
	DisplayName string
	BirthDate   *time.Time
	// AgeYears vale quando não há data de nascimento. Ver a migração 0005.
	AgeYears *int
	Sex      string
	HeightCm float64
	WeightKg *float64

	Experience     string
	WorkoutDays    []int
	WorkoutMinutes int
	WorkoutTime    string
	Equipment      []string

	DietStyle      string
	MealsPerDay    int
	FoodBudget     string
	FoodExclusions []string

	// MaxImpact é o tecto de pancada nas articulações. Nulo = sem tecto.
	MaxImpact *string
	// NutritionDetail é quanto da nutrição se mostra. Vazio = não mexer.
	NutritionDetail string
	// Nil quer dizer "não mexer". Ver a nota em `dto.ProfileRequest`.
	Preferences *repo.Preferences
}

// SavedProfile é o perfil como fica depois de gravado — o que a app desenha.
type SavedProfile struct {
	DisplayName    string   `json:"displayName"`
	Age            *int     `json:"age,omitempty"`
	Sex            string   `json:"sex"`
	HeightCm       *float64 `json:"heightCm,omitempty"`
	WeightKg       float64  `json:"weightKg,omitempty"`
	Experience     string   `json:"experience"`
	WorkoutDays    []int    `json:"workoutDays"`
	WorkoutMinutes int      `json:"workoutMinutes"`
	WorkoutTime    string   `json:"workoutTime"`
	Equipment      []string `json:"equipment"`
	// MaxImpact ausente quer dizer sem tecto — a maioria.
	MaxImpact *string `json:"maxImpact,omitempty"`
	// NutritionDetail é quanto da nutrição se mostra: "simple" ou "detailed".
	NutritionDetail string   `json:"nutritionDetail"`
	DietStyle       string   `json:"dietStyle"`
	MealsPerDay     int      `json:"mealsPerDay"`
	FoodBudget      string   `json:"foodBudget"`
	FoodExclusions  []string `json:"foodExclusions"`
	PhotoURL        string   `json:"photoUrl,omitempty"`
	Complete        bool     `json:"complete"`

	PinnedExercises       []string                         `json:"pinnedExercises"`
	ExcludedExercises     []string                         `json:"excludedExercises"`
	ExercisePrescriptions map[string]training.Prescription `json:"exercisePrescriptions"`
}

// Save grava o perfil e devolve-o como ficou.
//
// Devolver em vez de responder 204: a app desenha o que o servidor diz, e
// mandá-la pedir outra vez o que acabou de escrever é um ecrã em branco entre
// os dois pedidos.
func (p *Profiles) Save(ctx ctxLike, userID string, in SaveProfileInput, now time.Time) (SavedProfile, error) {
	c := asContext(ctx)
	if err := p.repo.Save(c, userID, repo.ProfileInput{
		DisplayName: in.DisplayName, BirthDate: in.BirthDate, AgeYears: in.AgeYears, Sex: in.Sex,
		HeightCm: in.HeightCm, WeightKg: in.WeightKg,
		Experience: in.Experience, WorkoutDays: in.WorkoutDays,
		WorkoutMinutes: in.WorkoutMinutes, WorkoutTime: in.WorkoutTime,
		Equipment: in.Equipment, DietStyle: in.DietStyle,
		MaxImpact: tectoDeImpacto(in.MaxImpact), MaxImpactDado: in.MaxImpact != nil,
		NutritionDetail: in.NutritionDetail,
		MealsPerDay:     in.MealsPerDay, FoodBudget: in.FoodBudget,
		FoodExclusions: in.FoodExclusions,
	}, now); err != nil {
		return SavedProfile{}, err
	}
	// Nil quer dizer "não mexer": um cliente antigo que não as envie não apaga
	// as escolhas de quem ainda não actualizou a app.
	if in.Preferences != nil && p.prefs != nil {
		if err := p.prefs.Replace(c, userID, *in.Preferences); err != nil {
			return SavedProfile{}, err
		}
	}
	return p.Read(c, userID)
}

// Uploader envia a imagem e devolve os endereços já enquadrados.
//
// Interface e não o cliente concreto: o serviço não tem de saber que existe uma
// Cloudinary, e os testes não têm de falar com a internet para provar que a
// fotografia é gravada.
type Uploader interface {
	UploadAvatar(ctx context.Context, image []byte, publicID string) (full, thumb string, faceFound bool, err error)
	// DeleteAvatar apaga a imagem. Uma imagem que já lá não está **não** é
	// erro: quem pede para remover quer que deixe de existir, e já não existe.
	DeleteAvatar(ctx context.Context, publicID string) error
}

// ErrNoUploader é o que se diz quando não há para onde enviar.
var ErrNoUploader = errors.New("sem serviço de imagens configurado")

// SavePhoto envia a fotografia e guarda o endereço.
//
// O `publicID` é o identificador do utilizador: substituir é o comportamento
// certo — quem troca a fotografia não quer duas, e sem isto cada troca deixava
// uma cópia órfã na conta para sempre.
func (p *Profiles) SavePhoto(ctx ctxLike, userID string, image []byte, now time.Time) (SavedProfile, error) {
	if p.uploader == nil {
		return SavedProfile{}, ErrNoUploader
	}
	c := asContext(ctx)

	full, _, _, err := p.uploader.UploadAvatar(c, image, userID)
	if err != nil {
		return SavedProfile{}, err
	}

	// Só se grava depois de a imagem existir lá: gravar primeiro deixaria um
	// endereço a apontar para nada se o envio falhasse.
	found, err := p.repo.SetPhoto(c, userID, &full, now)
	if err != nil {
		return SavedProfile{}, err
	}
	if !found {
		return SavedProfile{}, repo.ErrNoProfile
	}
	return p.Read(c, userID)
}

// RemovePhoto tira a fotografia do perfil e apaga-a.
//
// **Primeiro a imagem, depois o perfil**, e a ordem importa. Ao contrário, uma
// falha ao apagar deixava o ficheiro na Cloudinary com o perfil já limpo — e
// ninguém voltaria a saber que ele lá estava. O endereço é público a quem o
// tenha: "remover" tem de querer dizer removido.
//
// O risco da ordem escolhida é o oposto — imagem apagada e perfil ainda a
// apontar-lhe — e é o menos mau: vê-se logo, e repetir resolve.
func (p *Profiles) RemovePhoto(ctx ctxLike, userID string, now time.Time) (SavedProfile, error) {
	c := asContext(ctx)

	if p.uploader != nil {
		if err := p.uploader.DeleteAvatar(c, userID); err != nil {
			return SavedProfile{}, err
		}
	}

	found, err := p.repo.SetPhoto(c, userID, nil, now)
	if err != nil {
		return SavedProfile{}, err
	}
	if !found {
		return SavedProfile{}, repo.ErrNoProfile
	}
	return p.Read(c, userID)
}

// Read devolve o perfil gravado. `ErrWeightMissing` não é impedimento: o perfil
// existe, só ainda não tem pesagem.
func (p *Profiles) Read(ctx ctxLike, userID string) (SavedProfile, error) {
	row, err := p.repo.Profile(asContext(ctx), userID)
	if err != nil && !errors.Is(err, repo.ErrNoWeight) {
		return SavedProfile{}, fmt.Errorf("ler perfil: %w", err)
	}
	sex := "unspecified"
	if row.Sex != nil {
		sex = *row.Sex
	}
	out := SavedProfile{
		DisplayName: row.DisplayName, Age: row.Age, Sex: sex,
		HeightCm: row.HeightCm, WeightKg: row.WeightKg,
		Experience: row.Experience, WorkoutDays: nonNilInts(row.WorkoutDays),
		WorkoutMinutes: row.WorkoutMinutes, WorkoutTime: row.WorkoutTime,
		Equipment: nonNil(row.Equipment), MaxImpact: row.MaxImpact,
		NutritionDetail: row.NutritionDetail, DietStyle: row.DietStyle,
		MealsPerDay: row.MealsPerDay, FoodBudget: row.FoodBudget,
		FoodExclusions: nonNil(row.FoodExclusions), PhotoURL: deref(row.PhotoURL),
		Complete: row.Complete,
		// Listas vazias e não `null`: ver a nota logo abaixo.
		PinnedExercises:       []string{},
		ExcludedExercises:     []string{},
		ExercisePrescriptions: map[string]training.Prescription{},
	}

	if p.prefs != nil {
		prefs, err := p.prefs.Read(asContext(ctx), userID)
		if err != nil {
			return SavedProfile{}, fmt.Errorf("ler preferências: %w", err)
		}
		out.PinnedExercises = prefs.Pinned
		out.ExcludedExercises = prefs.Excluded
		out.ExercisePrescriptions = prefs.Prescriptions
	}
	return out, nil
}

// JSON com `null` onde a app espera uma lista faz `map` rebentar no cliente.
// Uma lista vazia é uma lista.
func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func deref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func nonNilInts(v []int) []int {
	if v == nil {
		return []int{}
	}
	return v
}

type ctxLike = interface {
	Deadline() (time.Time, bool)
	Done() <-chan struct{}
	Err() error
	Value(any) any
}

func asContext(ctx ctxLike) context.Context {
	if c, ok := ctx.(context.Context); ok {
		return c
	}
	return context.Background()
}

func (p *Profiles) Profile(ctx ctxLike, userID string) (CreateGoalInput, error) {
	row, err := p.repo.Profile(asContext(ctx), userID)
	if err != nil {
		return CreateGoalInput{}, err
	}
	return CreateGoalInput{
		UserID:          userID,
		CurrentWeightKg: row.WeightKg,
		HeightCm:        row.HeightCm,
		Age:             row.Age,
		Sex:             row.Sex,
		DaysPerWeek:     len(row.WorkoutDays),
		SessionMinutes:  row.WorkoutMinutes,
		Experience:      row.Experience,
		NutritionGoal:   p.defaultNutritionGoal,
		MealsPerDay:     row.MealsPerDay,
	}, nil
}

func (p *Profiles) TrainingProfile(ctx ctxLike, userID string, day time.Time) (TodayInput, error) {
	c := asContext(ctx)
	row, err := p.repo.Profile(c, userID)
	// Montar o treino de hoje não precisa de peso: precisa de experiência,
	// equipamento e tempo. Recusar por falta de uma pesagem seria fechar o
	// treino a quem ainda não se pesou — e o treino é o que traz a pessoa de
	// volta para se pesar.
	if err != nil && !errors.Is(err, repo.ErrNoWeight) {
		return TodayInput{}, err
	}

	// O plano gravado manda, quando existe. Ainda não existe: nada escreve
	// `planned_session`. Até escrever, o rótulo deriva-se dos dias de treino
	// — a mesma conta que o telemóvel faz, agora feita aqui.
	label := training.PlanLabelOn(row.WorkoutDays, day)
	if planned, ok := p.repo.PlanLabelOn(c, userID, day); ok {
		label = planned
	}
	out := TodayInput{
		PlanLabel:      label,
		Experience:     row.Experience,
		Equipment:      row.Equipment,
		WorkoutMinutes: row.WorkoutMinutes,
		LocalDay:       day,
	}
	// O tecto de impacto é uma condição de quem treina, como o equipamento: um
	// exercício acima dele não existe para esta pessoa, e o motor escolhe outro
	// no lugar em vez de deixar um buraco no treino.
	if row.MaxImpact != nil {
		out.MaxImpact = *row.MaxImpact
	}
	// O treinador da equipa muda o treino — ver `training/metodo.go`. Um
	// nutricionista na equipa não entra aqui: não mexe no plano de treino.
	if p.equipa != nil {
		if treinador, err := p.equipa.Treinador(c, userID); err == nil {
			out.Specialist = treinador
		}
	}

	// As escolhas da pessoa entram no motor. Sem elas o servidor montava uma
	// sessão diferente da que o telemóvel tem, o Modo Foco reparava e voltava
	// ao motor local — quem personalizava o plano deixava de receber o pacote.
	if p.prefs != nil {
		prefs, err := p.prefs.Read(c, userID)
		if err != nil {
			return TodayInput{}, fmt.Errorf("ler preferências: %w", err)
		}
		out.Pinned = prefs.Pinned
		out.Excluded = prefs.Excluded
		out.Prescriptions = prefs.Prescriptions
	}
	return out, nil
}

// NutritionProfile dá ao serviço de nutrição o que o pedido não traz.
//
// A dieta, o orçamento e as exclusões vêm do perfil — recebê-los no corpo era
// deixar o cliente escolher o que come hoje, e isso é o plano que decide.
func (p *Profiles) NutritionProfile(ctx ctxLike, userID string, day time.Time) (NutritionTodayInput, error) {
	c := asContext(ctx)
	row, err := p.repo.Profile(c, userID)
	// Ao contrário do treino, aqui o peso é preciso: sem ele não há proteína
	// por quilo nem gasto. Um plano alimentar sem peso não é um plano.
	if err != nil {
		return NutritionTodayInput{}, err
	}

	return NutritionTodayInput{
		UserID: userID,
		Diet: nutrition.DietProfile{
			Style:       nutrition.DietStyle(row.DietStyle),
			MealsPerDay: row.MealsPerDay,
			Budget:      nutrition.Budget(row.FoodBudget),
			Exclusions:  row.FoodExclusions,
		},
		Training: nutrition.TrainingLoad{
			SessionsPerWeek: len(row.WorkoutDays),
			WorkoutTime:     row.WorkoutTime,
		},
		// A segunda-feira é o dia 0, como no plano e no perfil.
		TrainsToday: treinaEm(row.WorkoutDays, (int(day.Weekday())+6)%7),

		WeightKg:       row.WeightKg,
		HeightCm:       row.HeightCm,
		Age:            row.Age,
		Sex:            row.Sex,
		DaysPerWeek:    len(row.WorkoutDays),
		SessionMinutes: row.WorkoutMinutes,

		LocalDay: day,
	}, nil
}

func treinaEm(days []int, index int) bool {
	for _, d := range days {
		if d == index {
			return true
		}
	}
	return false
}

// TrainingDaysOf são os dias de treino da semana, com a segunda em 0.
//
// Existe à parte do perfil inteiro porque a adesão só precisa disto — e ler o
// perfil todo para saber os dias fazia o progresso depender de campos que não
// usa, como a fotografia.
func (p *Profiles) TrainingDaysOf(ctx ctxLike, userID string) ([]int, error) {
	row, err := p.repo.Profile(asContext(ctx), userID)
	if err != nil && !errors.Is(err, repo.ErrNoWeight) {
		return nil, err
	}
	return nonNilInts(row.WorkoutDays), nil
}

// EquipmentOf devolve o equipamento e o nível, que é o que as aulas precisam
// para não prometerem o que a pessoa não consegue fazer.
func (p *Profiles) EquipmentOf(ctx ctxLike, userID string) ([]string, string, error) {
	row, err := p.repo.Profile(asContext(ctx), userID)
	if err != nil && !errors.Is(err, repo.ErrNoWeight) {
		return nil, "", err
	}
	return nonNil(row.Equipment), row.Experience, nil
}

/*
 * tectoDeImpacto traduz o que veio do pedido para o que a coluna guarda.
 *
 * `""` é a pessoa a tirar o tecto, e um enum não tem valor vazio: tem de ir
 * `NULL`. Sem isto, tirar o tecto rebentava com um erro de enum — e o erro
 * aparecia como 500 a quem só queria voltar a fazer saltos.
 */
func tectoDeImpacto(v *string) *string {
	if v == nil || *v == "" {
		return nil
	}
	return v
}

/*
 * ComEquipa liga o serviço à equipa, para o treinador poder mudar o treino.
 *
 * Separado do construtor para os arneses de teste que não precisam dela não
 * terem de a montar — e porque acrescentar um parâmetro a `NewProfiles` era
 * mexer em todos eles para nada.
 */
func (p *Profiles) ComEquipa(e *repo.SpecialistRepo) *Profiles {
	p.equipa = e
	return p
}
