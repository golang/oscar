// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gerrit

// The types stored in Gerrit.

// A ChangeInfo is the information recorded for a change.
// This describes Gerrit JSON data.
type ChangeInfo struct {
	// ID of the change, currently <project>~<number>.
	ID string `json:"id,omitempty"`

	// Triplet ID, currently <project>~<branch>~<number>.
	TripletID string `json:"triplet_id,omitempty"`

	// The name of the project.
	Project string `json:"project,omitempty"`

	// The name of the target branch.
	// The refs/heads/ prefix is omitted.
	Branch string `json:"branch,omitempty"`

	// The topic to which this change belongs.
	Topic string `json:"topic,omitempty"`

	// The map that maps account IDs to AttentionSetInfo of that
	// account. Those are all accounts that are currently in the
	// attention set.
	AttentionSet map[int]AttentionSetInfo `json:"attention_set,omitempty"`

	// The map that maps account IDs to AttentionSetInfo of that
	// account. Those are all accounts that were in the attention
	// set but were removed. The AttentionSetInfo is the latest
	// and most recent removal of the account from the attention set.
	RemovedFromAttentionSet map[int]AttentionSetInfo `json:"removed_from_attention_set,omitempty"`

	// List of hashtags that are set on the change.
	Hashtags []string `json:"hashtags,omitempty"`

	// The Change-Id of the change.
	ChangeID string `json:"change_id"`

	// The subject of the change (header line of the commit message).
	Subject string `json:"subject"`

	// The status of the change (NEW, MERGED, ABANDONED).
	Status string `json:"status"`

	// The timestamp of when the change was created.
	Created TimeStamp `json:"created"`

	// The timestamp of when the change was last updated.
	Updated TimeStamp `json:"updated"`

	// The timestamp of when the change was submitted.
	Submitted TimeStamp `json:"submitted,omitempty"`

	// The user who submitted the change.
	Submitter *AccountInfo `json:"submitter,omitempty"`

	// The submit type of the change. Not set for merged changes.
	SubmitType string `json:"submit_type,omitempty"`

	// Whether the change has been approved by the project submit rules.
	Submittable bool `json:"submittable,omitempty"`

	// Number of inserted lines.
	Insertions int `json:"insertions"`

	// Number of deleted lines.
	Deletions int `json:"deletions"`

	// Total number of inline comments across all patch sets.
	TotalCommentCount int `json:"total_comment_count"`

	// Number of unresolved inline comment threads across all patch sets.
	UnresolvedCommentCount int `json:"unresolved_comment_count,omitempty"`

	// The change number.
	Number int `json:"_number"`

	// The virtual id number is globally unique. For local
	// changes, it is equal to the _number attribute. For imported
	// changes, the original _number is processed through a
	// function designed to prevent conflicts with local change
	// numbers. Note that its usage is intended solely for
	// Gerrit’s internals and UI, and adoption outside these
	// scenarios is not advised.
	VirtualIDNumber int `json:"virtual_id_number"`

	// The owner of the change.
	Owner *AccountInfo `json:"owner"`

	// List of the [SubmitRecordInfo] containing the submit records
	// for the change at the latest patchset.
	SubmitRecords []SubmitRecordInfo `json:"submit_records"`

	// The labels of the change as a map that maps the label names
	// to LabelInfo entries.
	Labels map[string]LabelInfo `json:"labels,omitempty"`

	// The reviewers as a map that maps a reviewer state to a list
	// of AccountInfo entities. Possible reviewer states are:
	// REVIEWER: Users with at least one non-zero vote on the change.
	// CC: Users that were added to the change, but have not voted.
	Reviewers map[string][]*AccountInfo `json:"reviewers,omitempty"`

	// Updates to reviewers that have been made while the change
	// was in the WIP state. Only present on WIP changes and only
	// if there are pending reviewer updates to report. These are
	// reviewers who have not yet been notified about being added
	// to or removed from the change. Possible states are:
	// REVIEWER: Users with at least one non-zero vote on the change.
	// CC: Users that were added to the change, but have not voted.
	// REMOVED: Users that were previously reviewers on the
	// change, but have been removed.
	PendingReviewers map[string][]*AccountInfo `json:"pending_reviewers,omitempty"`

	// Messages associated with the change.
	Messages []ChangeMessageInfo `json:"messages,omitempty"`

	// The number of the current patch set of this change.
	CurrentRevisionNumber int `json:"current_revision_number"`

	// The commit ID of the current patch set of this change.
	CurrentRevision string `json:"current_revision,omitempty"`

	// All patch sets of this change as a map that maps the commit
	// ID of the patch set to a [RevisionInfo] entity.
	Revisions map[string]RevisionInfo `json:"revisions,omitempty"`

	// The SHA-1 of the NoteDb meta ref.
	MetaRevID string `json:"meta_rev_id,omitempty"`

	// When present, change is marked as private.
	IsPrivate bool `json:"is_private,omitempty"`

	// When present, change is marked as Work In Progress.
	WorkInProgress bool `json:"work_in_progress,omitempty"`

	// When present, change has been marked Ready at some point in time.
	HasReviewStarted bool `json:"has_review_started,omitempty"`

	// The change number of the change that this change reverts.
	RevertOf int `json:"revert_of,omitempty"`

	// ID of the submission of this change. Only set if the status
	// is MERGED. This ID is equal to the change number of the
	// change that triggered the submission. If the change that
	// triggered the submission also has a topic, it will be
	// "<id>-<topic>" of the change that triggered the
	// submission. The callers must not rely on the format of the
	// submission ID.
	SubmissionID string `json:"submission_id,omitempty"`

	// The change number of the change that this change was
	// cherry-picked from. Only set if the cherry-pick has been
	// done through the Gerrit REST API (and not if a
	// cherry-picked commit was pushed).
	CherryPickOfChange int `json:"cherry_pic_of_change,omitempty"`

	// The patchset number of the change that this change was
	// cherry-picked from. Only set if the cherry-pick has been
	// done through the Gerrit REST API (and not if a
	// cherry-picked commit was pushed).
	CherryPickOfPatchSet int `json:"cherry_pick_of_patch_set,omitempty"`

	// The remaining fields are defined by Gerrit but we don't
	// request their values.

	// CustomKeyedValues map[string]string `json:"custom_keyed_values,omitempty"`
	// Starred bool `json:"starred,omitempty"`
	// Reviewed bool `json:"reviewed,omitempty"`
	// Mergeable bool `json:"mergeable,omitempty"`
	// Actions map[string]*ActionInfo `json:"actions,omitempty"`
	// Requirements `json:"requirements,omitempty"`
	// SubmitRequirements []SubmitRequirementResultInfo `json:"submit_requirements,omitempty"`
	// PermittedLabels map[string][]string `json:"permitted_labels,omitempty"`
	// RemovableLabels map[string]*AccountInfo `json:"removable_labels,omitempty"`
	// RemovableReviewers []*AccountInfo `json:"removable_reviewers,omitempty"`
	// ReviewerUpdates []ReviewerUpdateInfo `json:"reviewer_updates,omitempty"`
	// TrackingIDs []TrackingIDInfo `json:"tracking_ids,omitempty"`
	// Problems []ProblemInfo `json:"problems,omitempty"`
	// ContainsGitConflicts bool `json:"contains_git_conflicts,omitempty"`
}

// AccountInfo contains information about an account.
// This describes Gerrit JSON data.
type AccountInfo struct {
	// The numeric ID of the account.
	AccountID int `json:"_account_id"`
	// The full name of the user.
	Name string `json:"name,omitempty"`
	// The display name of the user.
	DisplayName string `json:"display_name,omitempty"`
	// The email address the user prefers to be contacted through.
	Email string `json:"email,omitempty"`
	// The username of the user.
	UserName string `json:"username,omitempty"`
	// List of [AvatarInfo] entities that provide information about
	// avatar images of the account.
	Avatars []AvatarInfo `json:"avatars,omitempty"`
	// Status message of the account.
	Status string `json:"status,omitempty"`
	// Whether the account is inactive.
	Inactive bool `json:"inactive,omitempty"`
	// List of additional tags that this account has. The only
	// current tag an account can have is SERVICE_USER.
	Tags []string `json:"tags,omitempty"`

	// The remaining fields are defined by Gerrit but we don't
	// request their values.

	// SecondaryEmails []string `json:"secondary_emails,omitempty"`
}

// AvatarInfo holds Information about an avatar image of an account.
// This describes Gerrit JSON data.
type AvatarInfo struct {
	// The URL to the avatar image.
	URL string `json:"url"`
	// The height of the avatar image in pixels.
	Height int `json:"height"`
	// The width of the avatar image in pixels.
	Width int `json:"width,omitempty"`
}

// AttentionSetInfo describes users in the Gerrit attention set.
// This describes Gerrit JSON data.
type AttentionSetInfo struct {
	// The account.
	Account *AccountInfo `json:"account"`
	// The timestamp of the last update.
	LastUpdate TimeStamp `json:"last_update"`
	// The reason for adding or removing the user. If the update
	// was caused by another user, that account is represented by
	// account ID in reason as <GERRIT_ACCOUNT_18419> and the
	// corresponding AccountInfo can be found in reason_account field.
	Reason string `json:"reason"`
	// AccountInfo of the user who caused the update.
	ReasonAccount *AccountInfo `json:"reason_account"`
}

// SubmitRecordInfo holds results from a submit rule.
// This describes Gerrit JSON data.
type SubmitRecordInfo struct {
	// The name of the submit rule that created this submit record.
	// The submit rule is specified in the form of "$plugin~$rule"
	// where $plugin is the plugin name and $rule is the name of
	// the class that implemented the submit rule.
	RuleName string `json:"rule_name"`
	// OK, the change can be submitted.
	// NOT_READY, additional labels are required before submit.
	// CLOSED, closed changes cannot be submitted.
	// FORCED, the change was submitted bypassing the submit rule.
	// RULE_ERROR, rule code failed with an error.
	status string `json:"status"`
	// A list of labels, each containing the following fields.
	// * label: the label name.
	// * status:
	// * appliedBy:
	Labels []struct {
		// The label name.
		Label string `json:"label"`
		// The label status: {OK, REJECT, MAY, NEED, IMPOSSIBLE}.
		Status string `json:"status"`
		// The account that applied the vote to the label.
		AppliedBy *AccountInfo `json:"appliedBy"`
	} `json:"labels,omitempty"`
	// List of the requirements to be met before this change can
	// be submitted.
	Requirements []Requirement `json:"requirements,omitempty"`
	// When status is RULE_ERROR this message provides some text
	// describing the failure of the rule predicate.
	ErrorMessage string `json:"error_message,omitempty"`
}

// Requirement hold information about a requirement relative to a change.
// This describes Gerrit JSON data.
type Requirement struct {
	// Status of the requirement. Can be either OK, NOT_READY or RULE_ERROR.
	Status string `json:"status"`
	// A human readable reason.
	FallbackText string `json:"fallback_text"`
	// Alphanumerical (plus hyphens or underscores) string to
	// identify what the requirement is and why it was
	// triggered. Can be seen as a class: requirements sharing the
	// same type were created for a similar reason, and the data
	// structure will follow one set of rules.
	Type string `json:"type"`
}

// LabelInfo holds information about a label on a change, always
// corresponding to the current patch set.
// This describes Gerrit JSON data.
type LabelInfo struct {
	// Whether the label is optional. Optional means the label may
	// be set, but it’s neither necessary for submission nor does
	// it block submission if set.
	Optional bool `json:"optional,omitempty"`
	// The description of the label.
	Description string `json:"description,omitempty"`
	// One user who approved this label on the change (voted the
	// maximum value).
	Approved *AccountInfo `json:"approved,omitempty"`
	// One user who rejected this label on the change (voted the
	// minimum value) .
	Rejected *AccountInfo `json:"rejected,omitempty"`
	// One user who recommended this label on the change (voted
	// positively, but not the maximum value).
	Recommended *AccountInfo `json:"recommended,omitempty"`
	// One user who disliked this label on the change (voted
	// negatively, but not the minimum value).
	Disliked *AccountInfo `json:"disliked,omitempty"`
	// If true, the label blocks submit operation.
	Blocking bool `json:"blocking,omitempty"`
	// The voting value of the user who recommended/disliked this
	// label on the change if it is not “+1”/“-1”.
	Value int `json:"value,omitempty"`
	// The default voting value for the label. This value may be
	// outside the range specified in permitted_labels.
	DefaultValue int `json:"default_value,omitempty"`
	// A list of integers containing the vote values applied to
	// this label at the latest patchset.
	Votes []int `json:"votes,omitempty"`
	// List of all approvals for this label. Items in this list
	// may not represent actual votes cast by users; if a user
	// votes on any label, a corresponding ApprovalInfo will
	// appear in this list for all labels.
	All []*ApprovalInfo `json:"all,omitempty"`
	// A map of all values that are allowed for this label. The
	// map maps the values (“-2”, “-1”, " `0`", “+1”, “+2”) to the
	// value descriptions.
	Values map[string]string `json:"values,omitempty"`
}

// ApprovalInfo holds information about an approval from a user for a
// label on a change.
// This describes Gerrit JSON data.
type ApprovalInfo struct {
	// The account that approved.
	AccountInfo
	// The vote that the user has given for the label. If present
	// and zero, the user is permitted to vote on the label. If
	// absent, the user is not permitted to vote on that label.
	Value int `json:"value,omitempty"`
	// The VotingRangeInfo the user is authorized to vote on that
	// label. If present, the user is permitted to vote on the
	// label regarding the range values. If absent, the user is
	// not permitted to vote on that label.
	PermittedVotingRange VotingRangeInfo `json:"permitted_voting_range,omitempty"`
	// The time and date describing when the approval was made.
	Date TimeStamp `json:"date,omitempty"`
	// Value of the tag field from ReviewInput set while posting
	// the review. Votes/comments that contain tag with
	// 'autogenerated:' prefix can be filtered out in the web UI.
	Tag string `json:"tag,omitempty"`
	// If true, this vote was made after the change was submitted.
	PostSubmit bool `json:"post_submit,omitempty"`
}

// VotingRangeInfo describes the continuous voting range from min to
// max values.
// This describes Gerrit JSON data.
type VotingRangeInfo struct {
	// The minimum voting value.
	Min int `json:"min"`
	// The maximum voting value.
	Max int `json:"max"`
}

// ChangeMessageInfo holds information about a message attached to a change.
// This describes Gerrit JSON data.
type ChangeMessageInfo struct {
	// The ID of the message.
	ID string `json:"id"`
	// Author of the message as an AccountInfo entity.
	// Unset if written by the Gerrit system.
	Author *AccountInfo `json:"author,omitempty"`
	// Real author of the message as an AccountInfo entity.
	// Only set if the message was posted on behalf of another user.
	RealAuthor *AccountInfo `json:"real_author,omitempty"`
	// The timestamp this message was posted.
	Date TimeStamp `json:"date"`
	// The text left by the user or Gerrit system. Accounts are
	// served as account IDs inlined in the text as
	// <GERRIT_ACCOUNT_18419>. All accounts, used in message, can
	// be found in accounts_in_message field.
	Message string `json:"message"`
	// AccountInfo list, used in message.
	AccountsInMessage []*AccountInfo `json:"accounts_in_message,omitempty"`
	// Value of the tag field from ReviewInput set while posting
	// the review. Votes/comments that contain tag with
	// 'autogenerated:' prefix can be filtered out in the web UI.
	Tag string `json:"tag,omitempty"`
	// Which patchset (if any) generated this message.
	RevisionNumber int `json:"_revision_number,omitempty"`
}

// RevisionInfo contains information about a patch set.
// This describes Gerrit JSON data.
type RevisionInfo struct {
	// The change kind. Valid values are REWORK, TRIVIAL_REBASE,
	// TRIVIAL_REBASE_WITH_MESSAGE_UPDATE, MERGE_FIRST_PARENT_UPDATE,
	// NO_CODE_CHANGE, and NO_CHANGE.
	Kind string `json:"kind"`
	// The patch set number, or edit if the patch set is an edit.
	Number int `json:"_number"`
	// The timestamp of when the patch set was created.
	Created TimeStamp `json:"created"`
	// The uploader of the patch set as an AccountInfo entity.
	Uploader *AccountInfo `json:"uploader"`
	// The real uploader of the patch set as an AccountInfo entity.
	// Only set if the upload was done on behalf of another user.
	RealUploader *AccountInfo `json:"real_uploader,omitempty"`
	// The Git reference for the patch set.
	Ref string `json:"ref"`
	// The commit of the patch set as a CommitInfo entity.
	Commit *CommitInfo `json:"commit,omitempty"`
	// The parent commits of this patch-set commit as a list of
	// ParentInfo entities. In each parent, we include the target
	// branch name if the parent is a merged commit in the target
	// branch. Otherwise, we include the change and patch-set
	// numbers of the parent change.
	ParentsData []ParentInfo `json:"parents_data,omitempty"`
	// The name of the target branch that this revision is set to
	// be merged into.  Note that if the change is moved with the
	// Move Change endpoint, this field can be different for
	// different patchsets. For example, if the change is moved
	// and a new patchset is uploaded afterwards, the RevisionInfo
	// of the previous patchset will contain the old branch, but
	// the newer patchset will contain the new branch.
	Branch string `json:"branch,omitempty"`
	// The description of this patchset, as displayed in the
	// patchset selector menu. May be empty if no description is set.
	Description string `json:"description,omitempty"`

	// The remaining fields are defined by Gerrit but we don't
	// request their values.

	// Fetch []FetchInfo `json:"fetch,omitempty"`
	// Files map[string]FileInfo `json:"files,omitempty"`
	// Actions map[string]ActionInfo `json:"actions,omitempty"`
	// Reviewed bool `json:"reviewed,omitempty"`
	// CommitWithFooters string `json:"commit_with_footers,omitempty"`
	// PushCertificate PushCertificateInfo `json:"push_certificate,omitempty"`
}

// CommitInfo holds information about a commit.
// This describes Gerrit JSON data.
type CommitInfo struct {
	// The commit ID. Not set if included in a RevisionInfo entity
	// that is contained in a map which has the commit ID as key.
	Commit string `json:"commit,omitempty"`
	// The parent commits of this commit as a list of CommitInfo
	// entities. In each parent only the commit and subject fields
	// are populated.
	Parents []CommitInfo `json:"parents,omitempty"`
	// The author of the commit as a GitPersonInfo entity.
	Author *GitPersonInfo `json:"author,omitempty"`
	// The committer of the commit as a GitPersonInfo entity.
	Committer *GitPersonInfo `json:"committer,omitempty"`
	// The subject of the commit (header line of the commit message).
	Subject string `json:"subject"`
	// The commit message.
	Message string `json:"message,omitempty"`
	// Links to the patch set in external sites as a list of
	// WebLinkInfo entities.
	WebLinks []WebLinkInfo `json:"web_links,omitempty"`
	// Links to the commit in external sites for resolving
	// conflicts as a list of WebLinkInfo entities.
	ResolveConflictsWebLinks []WebLinkInfo `json:"resolve_conflicts_web_links,omitempty"`
}

// GitPersonInfo holds information about the author/committer of a commit.
// This describes Gerrit JSON data.
type GitPersonInfo struct {
	// The name of the author/committer.
	Name string `json:"name"`
	// The email address of the author/committer.
	Email string `json:"email"`
	// The timestamp of when this identity was constructed.
	Date TimeStamp `json:"date"`
	// The timezone offset from UTC of when this identity was constructed.
	TZ int `json:"tz"`
}

// WebLinkInfo describes a link to an external site.
// This describes Gerrit JSON data.
type WebLinkInfo struct {
	// The text to be linkified.
	Name string `json:"name"`
	// Tooltip to show when hovering over the link.
	Tooltip string `json:"tooltip,omitempty"`
	// The link URL.
	URL string `json:"url"`
	// URL to the icon of the link.
	ImageURL string `json:"image_url,omitempty"`
}

// ParentInfo holds information about the parent commit of a patch-set.
// This describes Gerrit JSON data.
type ParentInfo struct {
	// Name of the target branch into which the parent commit is merged.
	BranchName string `json:"branch_name,omitempty"`
	// The commit SHA-1 of the parent commit, or empty if the
	// current commit is root.
	CommitID string `json:"commit_id,omitempty"`
	// Set to true if the parent commit is merged into the target branch.
	IsMergedInTargetBranch bool `json:"is_merged_in_target_branch,omitempty"`
	// If the parent commit is a patch-set of another gerrit
	// change, this field will hold the change ID of the parent
	// change. Otherwise, will be empty.
	ChangeID string `json:"change_id,omitempty"`
	// If the parent commit is a patch-set of another gerrit
	// change, this field will hold the change number of the
	// parent change. Otherwise, will be zero.
	ChangeNumber int `json:"change_number,omitempty"`
	// If the parent commit is a patch-set of another gerrit
	// change, this field will hold the patch-set number of the
	// parent change. Otherwise, will be zero.
	PatchSetNumber int `json:"patch_set_number,omitempty"`
	// If the parent commit is a patch-set of another gerrit
	// change, this field will hold the change status of the
	// parent change. Otherwise, will be empty.
	ChangeStatus string `json:"change_status,omitempty"`
}

// CommentInfo holds information about an inline comment.
// This describes Gerrit JSON data.
type CommentInfo struct {
	// The patch set number for the comment; only set in contexts
	// where comments may be returned for multiple patch sets.
	PatchSet int `json:"patch_set,omitempty"`
	// The URL encoded UUID of the comment.
	ID string `json:"id"`
	// The file path for which the inline comment was done.
	// Not set if returned in a map where the key is the file path.
	Path string `json:"path,omitempty"`
	// The side on which the comment was added.
	// Allowed values are REVISION and PARENT.
	// If not set, the default is REVISION.
	Side string `json:"side,omitempty"`
	// The 1-based parent number. Used only for merge commits when
	// side == PARENT. When not set the comment is for the
	// auto-merge tree.
	Parent int `json:"parent,omitempty"`
	// The number of the line for which the comment was done.
	// If range is set, this equals the end line of the range.
	// If neither line nor range is set, it’s a file comment.
	Line int `json:"line,omitempty"`
	// The range of the comment as a CommentRange entity.
	Range *CommentRange `json:"range,omitempty"`
	// The URL encoded UUID of the comment to which this comment is a reply.
	InReplyTo string `json:"in_reply_to,omitempty"`
	// The comment message.
	Message string `json:"message,omitempty"`
	// The timestamp of when this comment was written.
	Updated TimeStamp `json:"updated"`
	// The author of the message as an AccountInfo entity.
	// Unset for draft comments, assumed to be the calling user.
	Author *AccountInfo `json:"author,omitempty"`
	// Value of the tag field from ReviewInput set while posting
	// the review.
	Tag string `json:"tag,omitempty"`
	// Whether or not the comment must be addressed by the
	// user. The state of resolution of a comment thread is stored
	// in the last comment in that thread chronologically.
	Unresolved bool `json:"unresolved,omitempty"`
	// The id of the change message that this comment is linked to.
	ChangeMessageID string `json:"change_message_id,omitempty"`
	// Hex commit SHA-1 (40 characters string) of the commit of
	// the patchset to which this comment applies.
	CommitID string `json:"commit_id,omitempty"`
	// Suggested fixes for this comment.
	FixSuggestions []FixSuggestionInfo `json:"fix_suggestions,omitempty"`

	// The remaining fields are defined by Gerrit but we don't
	// request their values.

	// ContextLines []ContextLine `json:"context_lines,omitempty"`
	// SourceContextType string `json:"source_content_type,omitempty"`
}

// FixSuggestionInfo represents a suggested fix.
// This describes Gerrit JSON data.
type FixSuggestionInfo struct {
	// The UUID of the suggested fix.
	FixID string `json:"fix_id,omitempty"`
	// A description of the suggested fix.
	Description string `json:"description"`
	// A list of FixReplacementInfo entities indicating how the
	// content of one or several files should be modified. Within
	// a file, they should refer to non-overlapping regions.
	Replacements []FixReplacementInfo `json:"replacements"`
}

// FixReplacementInfo describes how the content of a file should be
// replaced by another content.
// This describes Gerrit JSON data.
type FixReplacementInfo struct {
	// The path of the file which should be modified. Any file in
	// the repository may be modified. The commit message can be
	// modified via the magic file /COMMIT_MSG though only the
	// part below the generated header of that magic file can be
	// modified. References to the header lines will result in
	// errors when the fix is applied.
	Path string `json:"path"`
	// A CommentRange indicating which content of the file should
	// be replaced. Lines in the file are assumed to be separated
	// by the line feed character.
	Range CommentRange `json:"range"`
	// The content which should be used instead of the current one.
	Replacement string `json:"replacement"`
}

// CommentRange describes the range of an inline comments.  The
// comment range is a range from the start position, specified by
// start_line and start_character, to the end position, specified by
// end_line and end_character. The start position is inclusive and the
// end position is exclusive.
// This describes Gerrit JSON data.
type CommentRange struct {
	// The start line number of the range. (1-based)
	StartLine int `json:"start_line"`
	// The character position in the start line. (0-based)
	StartCharacter int `json:"start_character"`
	// The end line number of the range. (1-based)
	EndLine int `json:"end_line"`
	// The character position in the end line. (0-based)
	EndCharacter int `json:"end_character"`
}
