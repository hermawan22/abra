package ai

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode"
)

const defaultLocalDimensions = 384

type LocalProvider struct {
	name       string
	dimensions int
}

func NewLocalProvider(config LocalConfig) (*LocalProvider, error) {
	if config.Dimensions == 0 {
		config.Dimensions = defaultLocalDimensions
	}
	provider := &LocalProvider{
		name:       config.Name,
		dimensions: config.Dimensions,
	}
	if provider.name == "" {
		provider.name = "local"
	}
	if err := provider.Validate(); err != nil {
		return nil, err
	}
	return provider, nil
}

func (p *LocalProvider) Name() string {
	return p.name
}

func (p *LocalProvider) Kind() ProviderKind {
	return ProviderLocal
}

func (p *LocalProvider) Validate() error {
	if p.dimensions <= 0 {
		return fmt.Errorf("%w: local dimensions must be positive", ErrInvalidConfig)
	}
	return nil
}

func (p *LocalProvider) Embed(_ context.Context, request EmbeddingRequest) (EmbeddingResponse, error) {
	if err := validateEmbeddingRequest(request); err != nil {
		return EmbeddingResponse{}, err
	}
	dimensions := embeddingDimensions(request, p.dimensions)
	if dimensions <= 0 {
		return EmbeddingResponse{}, fmt.Errorf("%w: dimensions must be positive", ErrInvalidRequest)
	}

	embeddings := make([]Embedding, 0, len(request.Input))
	for index, input := range request.Input {
		vector := deterministicEmbedding(input, dimensions)
		if err := ValidateEmbeddingDimensions(vector, dimensions); err != nil {
			return EmbeddingResponse{}, err
		}
		embeddings = append(embeddings, Embedding{
			Index:      index,
			Vector:     vector,
			Dimensions: len(vector),
		})
	}

	return EmbeddingResponse{
		Provider:   p.name,
		Model:      "local-hash",
		Embeddings: embeddings,
	}, nil
}

func (p *LocalProvider) Extract(_ context.Context, request ExtractionRequest) (ExtractionResponse, error) {
	if strings.TrimSpace(request.Input) == "" {
		return ExtractionResponse{}, fmt.Errorf("%w: input is required", ErrInvalidRequest)
	}
	value := localExtractionValue(request.Input, request.Schema)
	validationErrors, err := ValidateJSONValue(value, request.Schema)
	if err != nil {
		return ExtractionResponse{}, err
	}
	raw, _ := json.Marshal(value)
	return ExtractionResponse{
		Provider:         p.name,
		Model:            "local-deterministic",
		Value:            value,
		Raw:              string(raw),
		ValidationErrors: validationErrors,
	}, nil
}

func deterministicEmbedding(input string, dimensions int) []float64 {
	vector := make([]float64, dimensions)
	for _, token := range tokenize(input) {
		sum := sha256.Sum256([]byte(token))
		bucket := int(binary.BigEndian.Uint32(sum[0:4]) % uint32(dimensions))
		sign := 1.0
		if sum[4]%2 == 1 {
			sign = -1.0
		}
		weight := 1.0 + float64(sum[5]%7)/10.0
		vector[bucket] += sign * weight
	}
	normalize(vector)
	return vector
}

func localExtractionValue(input string, schema JSONSchema) any {
	if len(schema) == 0 {
		return map[string]any{"text": input}
	}
	return localValueForSchema(input, map[string]any(schema), "")
}

func localValueForSchema(input string, schema map[string]any, propertyName string) any {
	types := schemaTypes(schema["type"])
	if len(types) == 0 {
		if _, ok := schema["properties"]; ok {
			types = []string{"object"}
		} else if _, ok := schema["items"]; ok {
			types = []string{"array"}
		} else {
			types = []string{"string"}
		}
	}
	switch types[0] {
	case "object":
		output := map[string]any{}
		properties := schemaMap(schema["properties"])
		for name, child := range properties {
			childSchema, ok := child.(map[string]any)
			if !ok {
				continue
			}
			output[name] = localValueForSchema(input, childSchema, name)
		}
		return output
	case "array":
		itemsSchema, _ := schema["items"].(map[string]any)
		if wantsStringArray(propertyName, itemsSchema) {
			values := claimCandidates(input)
			output := make([]any, 0, len(values))
			for _, value := range values {
				output = append(output, value)
			}
			return output
		}
		return []any{}
	case "number", "integer":
		return float64(0)
	case "boolean":
		return false
	case "null":
		return nil
	default:
		return input
	}
}

func wantsStringArray(propertyName string, itemsSchema map[string]any) bool {
	if len(itemsSchema) == 0 {
		return false
	}
	itemTypes := schemaTypes(itemsSchema["type"])
	if len(itemTypes) > 0 && itemTypes[0] != "string" {
		return false
	}
	name := strings.ToLower(propertyName)
	return name == "claims" || name == "facts" || name == "items" || name == "statements"
}

func claimCandidates(input string) []string {
	split := strings.FieldsFunc(input, func(r rune) bool {
		return r == '\n' || r == '.' || r == '!' || r == '?'
	})
	candidates := make([]string, 0, len(split))
	for _, candidate := range split {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" {
			candidates = append(candidates, candidate)
		}
	}
	if len(candidates) == 0 {
		return []string{strings.TrimSpace(input)}
	}
	return candidates
}

func tokenize(input string) []string {
	return strings.FieldsFunc(strings.ToLower(input), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '-')
	})
}

func normalize(vector []float64) {
	var sum float64
	for _, value := range vector {
		sum += value * value
	}
	length := math.Sqrt(sum)
	if length == 0 {
		return
	}
	for index := range vector {
		vector[index] = vector[index] / length
	}
}
