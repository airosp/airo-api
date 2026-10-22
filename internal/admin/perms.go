package admin

// Role é a função de um membro da equipa.
type Role string

const (
	RoleAdmin      Role = "admin"
	RoleContent    Role = "content"
	RoleNutrition  Role = "nutrition"
	RoleSupport    Role = "support"
	RoleSpecialist Role = "specialist"
)

var Roles = []Role{RoleAdmin, RoleContent, RoleNutrition, RoleSupport, RoleSpecialist}

func ValidRole(r string) bool {
	for _, x := range Roles {
		if string(x) == r {
			return true
		}
	}
	return false
}

// Area é uma parte do painel.
type Area string

const (
	AreaUsers       Area = "users"
	AreaUserRecord  Area = "userRecord"
	AreaSpecialists Area = "specialists"
	AreaClasses     Area = "classes"
	AreaTraining    Area = "training"
	AreaNutrition   Area = "nutrition"
	AreaAI          Area = "ai"
	AreaTeam        Area = "team"
)

// Level é o que uma função pode fazer numa área.
type Level string

const (
	Edit Level = "edit"
	View Level = "view"
	None Level = "none"
)

// AreaInfo é uma linha da matriz, com o nome que o ecrã mostra.
type AreaInfo struct {
	Key   Area           `json:"key"`
	Label string         `json:"label"`
	Of    map[Role]Level `json:"levels"`
}

/*
 * A matriz. Uma tabela e não `if`s espalhados: é a mesma que o ecrã da equipa
 * mostra, e o que se mostra tem de ser o que se aplica.
 *
 * A ficha de saúde de uma pessoa é `view` no máximo, para toda a gente: o
 * painel acompanha, não muda o plano de ninguém — quem aplica é a pessoa.
 * O especialista só vê as pessoas que o escolheram; esse corte faz-se na
 * consulta, não aqui.
 */
var Matrix = []AreaInfo{
	{AreaUsers, "Utilizadores — lista", map[Role]Level{RoleAdmin: Edit, RoleContent: View, RoleNutrition: View, RoleSupport: View, RoleSpecialist: View}},
	{AreaUserRecord, "Ficha de saúde de uma pessoa", map[Role]Level{RoleAdmin: View, RoleContent: None, RoleNutrition: View, RoleSupport: View, RoleSpecialist: View}},
	{AreaSpecialists, "Especialistas", map[Role]Level{RoleAdmin: Edit, RoleContent: Edit, RoleNutrition: View, RoleSupport: None, RoleSpecialist: None}},
	{AreaClasses, "Aulas, vídeos e playlists", map[Role]Level{RoleAdmin: Edit, RoleContent: Edit, RoleNutrition: None, RoleSupport: View, RoleSpecialist: Edit}},
	{AreaTraining, "Exercícios e modelos de treino", map[Role]Level{RoleAdmin: Edit, RoleContent: Edit, RoleNutrition: None, RoleSupport: None, RoleSpecialist: Edit}},
	{AreaNutrition, "Pratos, alimentos e dietas", map[Role]Level{RoleAdmin: Edit, RoleContent: View, RoleNutrition: Edit, RoleSupport: None, RoleSpecialist: Edit}},
	{AreaAI, "Inteligência e custos", map[Role]Level{RoleAdmin: Edit, RoleContent: None, RoleNutrition: View, RoleSupport: None, RoleSpecialist: None}},
	{AreaTeam, "Equipa e auditoria", map[Role]Level{RoleAdmin: Edit, RoleContent: None, RoleNutrition: None, RoleSupport: None, RoleSpecialist: None}},
}

// LevelOf devolve o que a função pode fazer na área. Desconhecido é `none`.
func LevelOf(r Role, a Area) Level {
	for _, row := range Matrix {
		if row.Key == a {
			if l, ok := row.Of[r]; ok {
				return l
			}
		}
	}
	return None
}

// Can diz se a função chega ao nível pedido.
func Can(r Role, a Area, want Level) bool {
	l := LevelOf(r, a)
	switch want {
	case Edit:
		return l == Edit
	case View:
		return l == Edit || l == View
	}
	return true
}

// Permissions é a matriz de uma função, para o `/me`.
func Permissions(r Role) map[Area]Level {
	out := make(map[Area]Level, len(Matrix))
	for _, row := range Matrix {
		out[row.Key] = LevelOf(r, row.Key)
	}
	return out
}
