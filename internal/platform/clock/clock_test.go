package clock

import (
	"testing"
	"time"
)

// Maputo é UTC+2 e não tem horário de verão — o caso real do produto.
var maputo = time.FixedZone("CAT", 2*60*60)

func TestTodayUsesUserDayNotUTC(t *testing.T) {
	// 01h00 em Maputo = 23h00 UTC do dia anterior. Contar por UTC partia a
	// sequência a quem treina de madrugada.
	night := time.Date(2026, 10, 2, 1, 0, 0, 0, maputo)
	c := NewFixed(night)

	got := c.Today(maputo)
	if got.Day() != 2 || got.Month() != time.October {
		t.Fatalf("dia local = %s, queria 2 de Outubro", got.Format("2 Jan"))
	}
	if utcDay := c.Today(time.UTC); utcDay.Day() != 1 {
		t.Fatalf("em UTC o mesmo instante é dia 1; obtive %d", utcDay.Day())
	}
}

func TestStartOfWeekIsMonday(t *testing.T) {
	cases := []struct{ day, want int }{
		{28, 28}, // segunda
		{29, 28}, // terça
		{1, 28},  // domingo 4 Out → segunda 28 Set
	}
	for _, tc := range cases {
		month := time.September
		if tc.day == 1 {
			month, tc.day = time.October, 4
		}
		got := StartOfWeek(time.Date(2026, month, tc.day, 15, 0, 0, 0, maputo), maputo)
		if got.Weekday() != time.Monday || got.Day() != tc.want {
			t.Fatalf("semana de %d/%s começa em %s, queria dia %d (segunda)",
				tc.day, month, got.Format("2 Jan (Mon)"), tc.want)
		}
	}
}

func TestWeekdayIndexMondayIsZero(t *testing.T) {
	want := map[time.Weekday]int{
		time.Monday: 0, time.Tuesday: 1, time.Wednesday: 2, time.Thursday: 3,
		time.Friday: 4, time.Saturday: 5, time.Sunday: 6,
	}
	for d := 0; d < 7; d++ {
		day := time.Date(2026, 9, 28+d, 12, 0, 0, 0, maputo) // 28 Set 2026 = segunda
		if got := WeekdayIndex(day); got != want[day.Weekday()] {
			t.Fatalf("%s → %d, queria %d", day.Weekday(), got, want[day.Weekday()])
		}
	}
}

func TestFixedAdvance(t *testing.T) {
	c := NewFixed(time.Date(2026, 10, 1, 8, 0, 0, 0, maputo))
	c.Advance(28 * 24 * time.Hour) // a viragem de um ciclo
	if c.Now().Day() != 29 || c.Now().Month() != time.October {
		t.Fatalf("depois de 4 semanas: %s", c.Now().Format("2 Jan"))
	}
}
