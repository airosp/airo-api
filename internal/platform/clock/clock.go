// Package clock isola "agora" do resto do sistema.
//
// Não é decoração. Toda a lógica de domínio da Airo depende do dia: a semana
// começa à segunda, um ciclo fecha ao fim de quatro semanas, a sequência quebra
// se faltar um dia, e nada se decide antes de dez dias de registo. Sem um
// relógio injectável, testar a viragem de um ciclo obrigaria a esperar quatro
// semanas.
package clock

import "time"

// Clock é a única fonte de "agora" em todo o servidor. Nenhum motor, serviço ou
// repositório chama time.Now() directamente.
type Clock interface {
	Now() time.Time

	// Today é o dia **local do utilizador**, e não date(Now()).
	//
	// A diferença importa: um treino às 23h30 em Maputo é 21h30 em UTC do mesmo
	// dia, mas um treino à 01h00 em Maputo é 23h00 UTC do dia anterior. Contar a
	// sequência por UTC partia-a a quem treina de madrugada.
	Today(loc *time.Location) time.Time
}

// System é o relógio real. Usado em produção e em mais lado nenhum.
type System struct{}

func (System) Now() time.Time { return time.Now() }

func (System) Today(loc *time.Location) time.Time {
	return startOfDay(time.Now(), loc)
}

// Fixed é um relógio parado, para testes. Avança só quando lhe mandam.
type Fixed struct{ T time.Time }

func NewFixed(t time.Time) *Fixed { return &Fixed{T: t} }

func (f *Fixed) Now() time.Time { return f.T }

func (f *Fixed) Today(loc *time.Location) time.Time { return startOfDay(f.T, loc) }

// Advance desloca o relógio. É o que permite testar "o que acontece na viragem
// do ciclo" sem esperar por ela.
func (f *Fixed) Advance(d time.Duration) { f.T = f.T.Add(d) }

func startOfDay(t time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	local := t.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
}

// StartOfWeek devolve a segunda-feira da semana de t, no fuso dado.
//
// Segunda e não domingo: o plano semanal da Airo começa à segunda, e
// `weekdayOf` no cliente usa a mesma convenção (0 = segunda).
func StartOfWeek(t time.Time, loc *time.Location) time.Time {
	day := startOfDay(t, loc)
	// time.Weekday tem domingo = 0; queremos segunda = 0.
	offset := (int(day.Weekday()) + 6) % 7
	return day.AddDate(0, 0, -offset)
}

// WeekdayIndex devolve 0 para segunda … 6 para domingo — a convenção usada em
// profile.workout_days e em plannedWeekday.
func WeekdayIndex(t time.Time) int { return (int(t.Weekday()) + 6) % 7 }
