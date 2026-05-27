package helpdesk

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

// ComplianceCheckInput is the input for the compliance check tool.
type ComplianceCheckInput struct {
	Content string `json:"content" jsonschema:"-" description:"The content to check for compliance"`
}

// piiPatterns defines regex patterns for detecting PII in content.
var piiPatterns = []struct {
	Name    string
	Pattern *regexp.Regexp
}{
	{Name: "phone", Pattern: regexp.MustCompile(`1[3-9]\d{9}`)},
	{Name: "email", Pattern: regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`)},
	{Name: "id_card", Pattern: regexp.MustCompile(`\d{17}[\dXx]`)},
	{Name: "bank_card", Pattern: regexp.MustCompile(`\d{16,19}`)},
}

// complianceRules defines rules that content must pass.
var complianceRules = []struct {
	Name    string
	Pattern *regexp.Regexp
}{
	{Name: "no_profanity", Pattern: regexp.MustCompile(`(?i)(fuck|shit|damn|妈的|靠|操)`)},
	{Name: "no_hate_speech", Pattern: regexp.MustCompile(`(?i)(歧视|种族|nazi|terrorist)`)},
}

// ComplianceCheckTool creates a tool that performs compliance checking including
// PII detection, rule validation, and optional LLM verification.
func ComplianceCheckTool(llm model.BaseChatModel) (tool.InvokableTool, error) {
	return utils.InferTool[*ComplianceCheckInput, string](
		"compliance_check",
		"Check content for compliance: PII detection, rule violations, "+
			"and optional LLM verification. "+
			"Returns JSON: {\"is_compliant\":true,\"violations\":[...],\"pii_found\":false}",
		func(ctx context.Context, input *ComplianceCheckInput) (string, error) {
			if input.Content == "" {
				return "", fmt.Errorf("empty content")
			}

			var violations []string
			piiFound := false

			// Step 1: PII scanning (regex, no LLM needed)
			var foundPII []string
			for _, p := range piiPatterns {
				if matches := p.Pattern.FindAllString(input.Content, -1); len(matches) > 0 {
					piiFound = true
					for _, m := range matches {
						foundPII = append(foundPII, fmt.Sprintf("%s:%s", p.Name, maskPII(p.Name, m)))
					}
				}
			}
			_ = foundPII // available for logging

			// Step 2: Compliance rule checking (regex, no LLM needed)
			for _, r := range complianceRules {
				if r.Pattern.MatchString(input.Content) {
					violations = append(violations, r.Name)
				}
			}

			// Step 3: Sanitize PII content
			sanitized := input.Content
			for _, p := range piiPatterns {
				sanitized = p.Pattern.ReplaceAllString(sanitized, "[REDACTED_"+p.Name+"]")
			}

			result := ComplianceResult{
				IsCompliant:      len(violations) == 0,
				Violations:       violations,
				PIIFound:         piiFound,
				SanitizedContent: sanitized,
				VerifiedByLLM:    false,
			}

			// Step 4: If violations found, optionally verify with LLM
			if len(violations) > 0 && llm != nil {
				verified, err := llmVerify(ctx, llm, input.Content, violations)
				if err == nil {
					result.IsCompliant = verified
					result.VerifiedByLLM = true
				}
			}

			return formatComplianceResult(&result), nil
		},
	)
}

// maskPII masks sensitive data for logging, showing only partial content.
func maskPII(piiType, value string) string {
	switch piiType {
	case "phone":
		if len(value) == 11 {
			return value[:3] + "****" + value[7:]
		}
	case "email":
		if at := strings.Index(value, "@"); at > 0 {
			return value[:1] + "***" + value[at:]
		}
	case "id_card":
		if len(value) >= 10 {
			return value[:4] + "**********" + value[len(value)-4:]
		}
	case "bank_card":
		if len(value) >= 8 {
			return value[:4] + "********" + value[len(value)-4:]
		}
	}
	return "****"
}

// llmVerify uses an LLM call to verify if the content is truly non-compliant
// in context (reduces false positives from regex matching).
func llmVerify(ctx context.Context, llm model.BaseChatModel, content string, violations []string) (bool, error) {
	sysPrompt := schema.SystemMessage(fmt.Sprintf(`You are a compliance verifier.
The following content was flagged for possible violations: %s.
Determine if the content is actually non-compliant in context.
Consider: Is it a false positive? Is it a quote or reference?
Respond with ONLY: {"is_compliant": true/false, "reason": "..."}`,
		strings.Join(violations, ", ")))

	prompt := schema.UserMessage(content)

	resp, err := llm.Generate(ctx, []*schema.Message{sysPrompt, prompt})
	if err != nil {
		return true, err
	}

	body := strings.TrimSpace(resp.Content)
	var compliant bool
	if _, err := fmt.Sscanf(body, `{"is_compliant": %t`, &compliant); err != nil {
		return true, nil
	}
	return compliant, nil
}

func formatComplianceResult(r *ComplianceResult) string {
	violations := "[]"
	if len(r.Violations) > 0 {
		violations = `["` + strings.Join(r.Violations, `","`) + `"]`
	}
	sanitized := strings.ReplaceAll(r.SanitizedContent, `\`, `\\`)
	sanitized = strings.ReplaceAll(sanitized, `"`, `\"`)
	return fmt.Sprintf(
		`{"is_compliant":%t,"violations":%s,"pii_found":%t,"sanitized_content":"%s","verified_by_llm":%t}`,
		r.IsCompliant, violations, r.PIIFound, sanitized, r.VerifiedByLLM,
	)
}
