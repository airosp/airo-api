//go:build devtools

// avaliar-ia mede o reconhecimento antes de ele chegar a alguém.
//
// Corre as amostras de texto — e, se houver, um directório de fotografias com
// o peso conhecido — contra o modelo configurado, pelo mesmo caminho de
// produção, e escreve quanto acertou, omitiu e inventou.
//
// ⚠️ **Gasta dinheiro**: cada amostra é uma chamada paga. E as fotografias
// são de pessoas — vivem fora do repositório, com autorização de quem as tirou.
//
//	AIRO_OPENAI_API_KEY=… AIRO_OPENAI_MODEL=… go run -tags devtools ./cmd/avaliar-ia
//	… go run -tags devtools ./cmd/avaliar-ia --fotos ~/avaliacao/pratos
//
// O directório de fotografias tem um `esperado.json`:
//
//	[{"file": "matapa1.jpg", "foods": ["matapa","xima"], "grams": {"xima": 240}}]
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/airosp/airo-api/internal/intelligence/evaluation"
	"github.com/airosp/airo-api/internal/platform/openai"
	ai "github.com/airosp/airo-api/internal/service/intelligence"
)

func main() {
	fotos := flag.String("fotos", "", "directório com fotografias e esperado.json")
	soFotos := flag.Bool("so-fotos", false, "saltar as amostras de texto")
	flag.Parse()

	client, err := openai.New(openai.Config{
		APIKey: os.Getenv("AIRO_OPENAI_API_KEY"), Model: os.Getenv("AIRO_OPENAI_MODEL"),
		BaseURL: os.Getenv("AIRO_OPENAI_BASE_URL"), Timeout: 60 * time.Second, MaxRetries: 2,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	ctx := context.Background()

	if !*soFotos {
		var sum evaluation.Summary
		var tokensIn, tokensOut int64
		fmt.Printf("── texto · %s ──\n", client.Model())
		for _, c := range evaluation.TextCases {
			res, call, err := ai.Evaluate(ctx, client, c.Text, nil, "")
			tokensIn, tokensOut = tokensIn+call.InputTokens, tokensOut+call.OutputTokens
			if err != nil {
				fmt.Printf("ERRO  %-55q %v\n", c.Text, err)
				continue
			}
			s := evaluation.ScoreDraft(res.Items, c.Foods, c.Grams)
			sum.Add(s)
			got := make([]string, 0, len(res.Items))
			for _, it := range res.Items {
				g := "?"
				if it.Grams != nil {
					g = fmt.Sprintf("%.0f", *it.Grams)
				}
				got = append(got, it.FoodID+":"+g)
			}
			marca := "ok   "
			if s.Omitted+s.Invented+s.InventedGrams > 0 {
				marca = "FALHA"
			}
			fmt.Printf("%s %-55q %s  %dms\n", marca, c.Text, strings.Join(got, " "), call.Latency.Milliseconds())
		}
		fmt.Printf("\nprecisão %.2f · cobertura %.2f · inventados %d · gramas inventadas %d · erro das gramas %.0f%% · tokens %d/%d\n\n",
			sum.Precision(), sum.Recall(), sum.Invented, sum.InventedGrams, sum.GramsError()*100, tokensIn, tokensOut)
	}

	if *fotos != "" {
		raw, err := os.ReadFile(filepath.Join(*fotos, "esperado.json"))
		if err != nil {
			fmt.Fprintln(os.Stderr, "esperado.json:", err)
			os.Exit(2)
		}
		var cases []evaluation.PhotoCase
		if err := json.Unmarshal(raw, &cases); err != nil {
			fmt.Fprintln(os.Stderr, "esperado.json:", err)
			os.Exit(2)
		}
		var sum evaluation.Summary
		fmt.Printf("── fotografias · %s ──\n", client.Model())
		for _, c := range cases {
			img, err := os.ReadFile(filepath.Join(*fotos, c.File))
			if err != nil {
				fmt.Printf("ERRO  %s: %v\n", c.File, err)
				continue
			}
			res, call, err := ai.Evaluate(ctx, client, "", img, "image/jpeg")
			if err != nil {
				fmt.Printf("ERRO  %s: %v\n", c.File, err)
				continue
			}
			s := evaluation.ScoreDraft(res.Items, c.Foods, c.Grams)
			sum.Add(s)
			fmt.Printf("%-24s acertos %d omitidos %d inventados %d erro gramas %.0f%% desconhecidos %d  %dms\n",
				c.File, s.Hits, s.Omitted, s.Invented, s.GramsError*100, len(res.Unknown), call.Latency.Milliseconds())
		}
		fmt.Printf("\nprecisão %.2f · cobertura %.2f · erro das gramas %.0f%%\n",
			sum.Precision(), sum.Recall(), sum.GramsError()*100)
	}
}
