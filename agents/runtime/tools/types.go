package tools

import (
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
)

type Handler func(arguments string) string

type Spec struct {
	Name        string
	Description string
	Parameters  map[string]any
	Handler     Handler
}

type field struct {
	Name        string
	Type        string
	Description string
	Required    bool
}

func BuildTools(specs []Spec) []responses.ToolUnionParam {
	tools := make([]responses.ToolUnionParam, 0, len(specs))
	for _, spec := range specs {
		tools = append(tools, responses.ToolUnionParam{
			OfFunction: &responses.FunctionToolParam{
				Name:        spec.Name,
				Description: openai.String(spec.Description),
				Parameters:  spec.Parameters,
			},
		})
	}
	return tools
}

func BuildHandlers(specs []Spec) map[string]Handler {
	handlers := make(map[string]Handler, len(specs))
	for _, spec := range specs {
		handlers[spec.Name] = spec.Handler
	}
	return handlers
}

func newTool(name, description string, handler Handler, fields ...field) Spec {
	return Spec{
		Name:        name,
		Description: description,
		Parameters:  objectSchemaFromFields(fields...),
		Handler:     handler,
	}
}

func reqString(name, description string) field {
	return field{Name: name, Type: "string", Description: description, Required: true}
}

func optInteger(name, description string) field {
	return field{Name: name, Type: "integer", Description: description, Required: false}
}

func objectSchemaFromFields(fields ...field) map[string]any {
	properties := make(map[string]any, len(fields))
	required := make([]string, 0, len(fields))
	for _, field := range fields {
		prop := map[string]any{
			"type": field.Type,
		}
		if field.Description != "" {
			prop["description"] = field.Description
		}
		properties[field.Name] = prop
		if field.Required {
			required = append(required, field.Name)
		}
	}

	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}
