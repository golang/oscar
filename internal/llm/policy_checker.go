// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llm

import (
	"context"
	"fmt"
)

// A PolicyChecker checks inputs and outputs to LLMs against
// safety policies.
type PolicyChecker interface {
	// SetPolicies sets the policies to evaluate in subsequent
	// calls to [Check]. If unset, use the implementation's default.
	SetPolicies([]*PolicyConfig)
	// CheckText evaluates the policies configured on this [PolicyChecker]
	// against the given text and returns a result for each [PolicyConfig].
	// If the text represents a model output, the prompt parts used to generate it
	// may optionally be provided as context. If the text represents a model input,
	// prompt should be empty.
	CheckText(ctx context.Context, text string, prompt ...Part) ([]*PolicyResult, error)
}

// A PolicyConfig is a policy to apply to an input or output to an LLM.
//
// Copied from "google.golang.org/api/checks/v1alpha" to avoid direct dependency.
type PolicyConfig struct {
	// PolicyType: Required. Type of the policy.
	PolicyType PolicyType
	// Threshold: Optional. Score threshold to use when deciding if the content is
	// violative or non-violative. If not specified, the default 0.5 threshold for
	// the policy will be used.
	Threshold float64
}

// A PolicyResult is the result of evaluating a policy against
// an input or output to an LLM.
//
// Copied from "google.golang.org/api/checks/v1alpha" to avoid direct dependency.
type PolicyResult struct {
	// PolicyType: Type of the policy.
	PolicyType PolicyType
	// Score: Final score for the results of this policy.
	Score float64
	// ViolationResult: Result of the classification for the policy.
	ViolationResult ViolationResult
}

type PolicyType string

// Possible values for [PolicyType].
const (
	// Default.
	PolicyTypeUnspecified = PolicyType("POLICY_TYPE_UNSPECIFIED")
	// The model facilitates, promotes or enables access to
	// harmful goods, services, and activities.
	PolicyTypeDangerousContent = PolicyType("DANGEROUS_CONTENT")
	// The model reveals an individual’s personal
	// information and data.
	PolicyTypePIISolicitingReciting = PolicyType("PII_SOLICITING_RECITING")
	// The model generates content that is malicious,
	// intimidating, bullying, or abusive towards another individual.
	PolicyTypeHarassment = PolicyType("HARASSMENT")
	// The model generates content that is sexually
	// explicit in nature.
	PolicyTypeSexuallyExplicit = PolicyType("SEXUALLY_EXPLICIT")
	// The model promotes violence, hatred, discrimination on the
	// basis of race, religion, etc.
	PolicyTypeHateSpeech = PolicyType("HATE_SPEECH")
	// The model provides or offers to facilitate access to
	// medical advice or guidance.
	PolicyTypeMedicalInfo = PolicyType("MEDICAL_INFO")
	// The model generates content that contains
	// gratuitous, realistic descriptions of violence or gore.
	PolicyTypeViolenceAndGore = PolicyType("VIOLENCE_AND_GORE")
	// The model generates profanity and obscenities.
	PolicyTypeObscenityAndProfanity = PolicyType("OBSCENITY_AND_PROFANITY")
)

// AllPolicyTypes returns a policy that, when passed to
// to [PolicyChecker.SetPolicies], configures the PolicyChecker
// to check for all available dangerous content types at the default threshold.
func AllPolicyTypes() []*PolicyConfig {
	return []*PolicyConfig{
		{PolicyType: PolicyTypeDangerousContent},
		{PolicyType: PolicyTypePIISolicitingReciting},
		{PolicyType: PolicyTypeHarassment},
		{PolicyType: PolicyTypeSexuallyExplicit},
		{PolicyType: PolicyTypeHateSpeech},
		{PolicyType: PolicyTypeMedicalInfo},
		{PolicyType: PolicyTypeViolenceAndGore},
		{PolicyType: PolicyTypeObscenityAndProfanity},
	}
}

type ViolationResult string

// Possible values for [ViolationResult].
const (
	// Unspecified result.
	ViolationResultUnspecified = ViolationResult("VIOLATION_RESULT_UNSPECIFIED")
	// The final score is greater or equal the input score
	// threshold.
	ViolationResultViolative = ViolationResult("VIOLATIVE")
	// The final score is smaller than the input score
	// threshold.
	ViolationResultNonViolative = ViolationResult("NON_VIOLATIVE")
	// There was an error and the violation result could
	// not be determined.
	ViolationResultClassificationError = ViolationResult("CLASSIFICATION_ERROR")
)

// IsViolative reports whether the policy result represents
// a violated policy.
func (pr *PolicyResult) IsViolative() bool {
	return pr.ViolationResult == ViolationResultViolative
}

// String returns a string representation of the policy result.
func (pr *PolicyResult) String() string {
	return fmt.Sprintf("%s: %s (%f)", pr.PolicyType, pr.ViolationResult, pr.Score)
}
