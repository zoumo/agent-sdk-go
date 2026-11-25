// SPDX-License-Identifier: Apache-2.0

package types

// AskForApprovalValue is the former name of the scalar approval policy enum.
//
// Deprecated: use ApprovalPolicy.
type AskForApprovalValue = ApprovalPolicy

const (
	// Deprecated: use ApprovalPolicyUntrusted.
	AskForApprovalValueUntrusted = ApprovalPolicyUntrusted
	// Deprecated: use ApprovalPolicyOnRequest.
	AskForApprovalValueOnRequest = ApprovalPolicyOnRequest
	// Deprecated: use ApprovalPolicyNever.
	AskForApprovalValueNever = ApprovalPolicyNever
)
