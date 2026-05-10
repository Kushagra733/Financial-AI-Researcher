package safety

import (
	"fmt"
	"strings"
)

// SafetyRuleChecker implements SafetyChecker interface
type SafetyRuleChecker struct {
	PromptRules    []string
	ResponseRules  []string
	BlockedKeywords []string
}

// NewSafetyRuleChecker creates a new safety checker instance
func NewSafetyRuleChecker() *SafetyRuleChecker {
	checker := &SafetyRuleChecker{
		PromptRules:     []string{},
		ResponseRules:   []string{},
		BlockedKeywords: []string{},
	}
	checker.loadDefaultRules()
	return checker
}

// loadDefaultRules loads default safety rules
func (s *SafetyRuleChecker) loadDefaultRules() {
	s.BlockedKeywords = []string{
		"malware",
		"ransomware",
		"illegal",
		"exploit",
		"hack",
	}
}

// CheckPrompt validates a prompt for safety issues
func (s *SafetyRuleChecker) CheckPrompt(prompt string) (bool, string, error) {
	lowerPrompt := strings.ToLower(prompt)
	
	for _, keyword := range s.BlockedKeywords {
		if strings.Contains(lowerPrompt, keyword) {
			return false, fmt.Sprintf("blocked keyword detected: %s", keyword), nil
		}
	}
	
	return true, "", nil
}

// CheckResponse validates a response for safety issues
func (s *SafetyRuleChecker) CheckResponse(response string) (bool, string, error) {
	lowerResponse := strings.ToLower(response)
	
	for _, keyword := range s.BlockedKeywords {
		if strings.Contains(lowerResponse, keyword) {
			return false, fmt.Sprintf("blocked keyword in response: %s", keyword), nil
		}
	}
	
	return true, "", nil
}

// AddRule adds a custom safety rule
func (s *SafetyRuleChecker) AddRule(rule string) {
	s.BlockedKeywords = append(s.BlockedKeywords, strings.ToLower(rule))
}
