package painel

import (
	"net/http"

	"github.com/airosp/airo-api/internal/admin"
	"github.com/airosp/airo-api/internal/transport/http/apierr"
)

// Register põe as rotas do painel no encaminhador.
func (a *API) Register(mux *http.ServeMux) {
	E, V := admin.Edit, admin.View

	// Públicas: quem ainda não entrou não tem sessão para mostrar.
	mux.HandleFunc("POST /v1/admin/auth/login", a.Login)
	mux.HandleFunc("POST /v1/admin/auth/verify", a.Verify)

	mux.HandleFunc("POST /v1/admin/auth/logout", a.authed(a.Logout))
	mux.HandleFunc("GET /v1/admin/me", a.authed(a.Me))
	mux.HandleFunc("GET /v1/admin/overview", a.authed(a.Overview))

	mux.HandleFunc("GET /v1/admin/users", a.can(admin.AreaUsers, V, a.UsersList))
	mux.HandleFunc("GET /v1/admin/users/{id}", a.can(admin.AreaUserRecord, V, a.UserRecord))

	mux.HandleFunc("GET /v1/admin/specialists", a.can(admin.AreaSpecialists, V, a.SpecialistsList))
	mux.HandleFunc("POST /v1/admin/specialists", a.can(admin.AreaSpecialists, E, a.SpecialistCreate))
	mux.HandleFunc("PUT /v1/admin/specialists/{id}", a.can(admin.AreaSpecialists, E, a.SpecialistUpdate))
	mux.HandleFunc("POST /v1/admin/specialists/{id}/photo", a.can(admin.AreaSpecialists, E, a.SpecialistPhoto))
	// Dar acesso ao painel é decidir quem entra: é da gestão da equipa.
	mux.HandleFunc("POST /v1/admin/specialists/{id}/invite", a.can(admin.AreaTeam, E, a.SpecialistInvite))

	mux.HandleFunc("GET /v1/admin/classes", a.can(admin.AreaClasses, V, a.ClassesList))
	mux.HandleFunc("POST /v1/admin/classes", a.can(admin.AreaClasses, E, a.ClassCreate))
	mux.HandleFunc("GET /v1/admin/classes/{id}", a.can(admin.AreaClasses, V, a.ClassGet))
	mux.HandleFunc("PUT /v1/admin/classes/{id}", a.can(admin.AreaClasses, E, a.ClassUpdate))
	mux.HandleFunc("POST /v1/admin/classes/{id}/upload", a.can(admin.AreaClasses, E, a.ClassUpload))
	mux.HandleFunc("POST /v1/admin/classes/{id}/publish", a.can(admin.AreaClasses, E, a.ClassPublish))
	mux.HandleFunc("DELETE /v1/admin/classes/{id}", a.can(admin.AreaClasses, E, a.ClassDelete))

	mux.HandleFunc("GET /v1/admin/playlists", a.can(admin.AreaClasses, V, a.PlaylistsList))
	mux.HandleFunc("POST /v1/admin/playlists", a.can(admin.AreaClasses, E, a.PlaylistCreate))
	mux.HandleFunc("PUT /v1/admin/playlists/{id}", a.can(admin.AreaClasses, E, a.PlaylistUpdate))
	mux.HandleFunc("DELETE /v1/admin/playlists/{id}", a.can(admin.AreaClasses, E, a.PlaylistDelete))

	mux.HandleFunc("GET /v1/admin/exercises", a.can(admin.AreaTraining, V, a.ExercisesList))
	mux.HandleFunc("POST /v1/admin/exercises", a.can(admin.AreaTraining, E, a.ExerciseCreate))
	mux.HandleFunc("PUT /v1/admin/exercises/{id}", a.can(admin.AreaTraining, E, a.ExerciseUpdate))

	mux.HandleFunc("GET /v1/admin/workout-templates", a.can(admin.AreaTraining, V, a.TemplatesList))
	mux.HandleFunc("POST /v1/admin/workout-templates", a.can(admin.AreaTraining, E, a.TemplateCreate))
	mux.HandleFunc("PUT /v1/admin/workout-templates/{id}", a.can(admin.AreaTraining, E, a.TemplateUpdate))
	mux.HandleFunc("DELETE /v1/admin/workout-templates/{id}", a.can(admin.AreaTraining, E, a.TemplateDelete))

	mux.HandleFunc("GET /v1/admin/foods", a.can(admin.AreaNutrition, V, a.FoodsList))
	mux.HandleFunc("POST /v1/admin/foods", a.can(admin.AreaNutrition, E, a.FoodCreate))
	mux.HandleFunc("PUT /v1/admin/foods/{id}", a.can(admin.AreaNutrition, E, a.FoodUpdate))

	mux.HandleFunc("GET /v1/admin/recipes", a.can(admin.AreaNutrition, V, a.RecipesList))
	mux.HandleFunc("POST /v1/admin/recipes", a.can(admin.AreaNutrition, E, a.RecipeCreate))
	mux.HandleFunc("POST /v1/admin/recipes/preview", a.can(admin.AreaNutrition, V, a.RecipePreview))
	mux.HandleFunc("PUT /v1/admin/recipes/{id}", a.can(admin.AreaNutrition, E, a.RecipeUpdate))
	mux.HandleFunc("POST /v1/admin/recipes/{id}/validate", a.can(admin.AreaNutrition, E, a.nutritionist(a.RecipeValidate)))

	mux.HandleFunc("GET /v1/admin/diet-models", a.can(admin.AreaNutrition, V, a.DietsList))
	mux.HandleFunc("POST /v1/admin/diet-models", a.can(admin.AreaNutrition, E, a.DietCreate))
	mux.HandleFunc("PUT /v1/admin/diet-models/{id}", a.can(admin.AreaNutrition, E, a.DietUpdate))
	mux.HandleFunc("POST /v1/admin/diet-models/{id}/status", a.can(admin.AreaNutrition, E, a.DietStatus))

	mux.HandleFunc("GET /v1/admin/team", a.can(admin.AreaTeam, V, a.TeamList))
	mux.HandleFunc("POST /v1/admin/team/invites", a.can(admin.AreaTeam, E, a.TeamInvite))
	mux.HandleFunc("PATCH /v1/admin/team/{id}", a.can(admin.AreaTeam, E, a.TeamUpdate))
	mux.HandleFunc("POST /v1/admin/team/{id}/signout", a.can(admin.AreaTeam, E, a.TeamSignOut))
	mux.HandleFunc("GET /v1/admin/audit", a.can(admin.AreaTeam, V, a.AuditLog))
}

// nutritionist: validar um prato é a assinatura da nutrição (ou da
// administração), não de quem só o pode editar.
func (a *API) nutritionist(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m, _ := memberOf(r.Context())
		if m.role() != admin.RoleNutrition && m.role() != admin.RoleAdmin {
			apierr.Write(w, apierr.Forbidden, "Só a nutrição valida pratos.", "")
			return
		}
		next(w, r)
	}
}
