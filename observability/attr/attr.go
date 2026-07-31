// Package attr defines the standard o11y attribute names
package attr

import semconv "go.opentelemetry.io/otel/semconv/v1.30.0"

// Prefix is the attribute name prefix for our formal attributes. Whilst this is not used in the list of supported
// attributes in this package it is included for other packages to use for assertions and detection, etc.
const Prefix = "com.circleci."

// Supported attribute names. We use the full name (not using the const Prefix) to be explicit and grep-able.

// Common circle entities alphabetical
const (
	ActorID        = "com.circleci.actor.id"
	GroupID        = "com.circleci.group.id"
	JobID          = "com.circleci.job.id"
	JobName        = "com.circleci.job.name"
	JobNumber      = "com.circleci.job.number"
	JobParallelism = "com.circleci.job.parallelism"
	JobStatus      = "com.circleci.job.status"
	JobType        = "com.circleci.job.type"
	Namespace      = "com.circleci.namespace"
	OrgID          = "com.circleci.org.id"
	OrgName        = "com.circleci.org.name"
	PipelineID     = "com.circleci.pipeline.id"
	PipelineNumber = "com.circleci.pipeline.number"
	PlanID         = "com.circleci.plan.id"
	PlanType       = "com.circleci.plan.type"
	ProjectID      = "com.circleci.project.id"
	ProjectName    = "com.circleci.project.name"
	ProjectType    = "com.circleci.project.type"
	TaskID         = "com.circleci.task.id"
	TaskIndex      = "com.circleci.task.index"
	UserID         = "com.circleci.user.id"
	UserLogin      = "com.circleci.user.login"
	WorkflowID     = "com.circleci.workflow.id"
	WorkflowStatus = "com.circleci.workflow.status"
	WorkflowTag    = "com.circleci.workflow.tag"
)

// Common attributes
const (
	// Common general fields not directly related to core entities

	InternalInfo    = "com.circleci.info"
	Key             = "com.circleci.key"
	KeyType         = "com.circleci.key.type"
	LoopName        = "com.circleci.loop.name"
	BackoffDuration = "com.circleci.loop.backoff.duration_ms"
	BackoffType     = "com.circleci.loop.backoff.type"

	// Generic fields from external systems

	ExternalID     = "com.circleci.external.id"
	ExternalIDType = "com.circleci.external.id.type"
	ExternalInfo   = "com.circleci.external.info"

	// Further details to augment the semantic conventions

	// MessagingMessageType can be used to represent a type of the message, typically extracted from the
	// message payload
	MessagingMessageType = "messaging.message.type"

	// Semconv VCS fields

	VCSRefHeadName  = string(semconv.VCSRefHeadNameKey)
	VCSRefHeadType  = string(semconv.VCSRefHeadTypeKey)
	VCSRepoFullURL  = string(semconv.VCSRepositoryURLFullKey)
	VCSRepoID       = "vcs.repository.id"
	VCSRepoName     = string(semconv.VCSRepositoryNameKey)
	VCSOwnerName    = "vcs.owner.name"
	VCSOwnerID      = "vcs.owner.id" // this is not Semconv defined
	VCSProviderName = "vcs.provider.name"
)
