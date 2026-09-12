package handlers

import "github.com/airosp/airo-api/internal/engine/training"

// countMainSets conta só as séries de trabalho. Aquecer e alongar não são
// séries — ver docs/backend/03-regras-de-negocio.md §3.
func countMainSets(steps []training.Step) int { return training.TotalSets(steps) }
