package event

// Messaging event kinds, published on JetStream subject MESSAGING.<kind>.
const (
	MessageCreated         = "message.created"
	ConversationRequested  = "conversation.requested"
	ConversationAccepted   = "conversation.accepted"
	ConversationDeclined   = "conversation.declined"
	ConversationRevoked    = "conversation.revoked"
	HelpdeskStatusChanged  = "helpdesk.status_changed"
	ConversationRead       = "conversation.read"
	BlockChanged           = "block.changed"
	PoolVoucherAdmitted    = "pool.voucher_admitted"
	SafeProposed           = "safe.proposed"
	SafeConfirmed          = "safe.confirmed"
	SafeApproved           = "safe.approved"
	SafeRejectionProposed  = "safe.rejection_proposed"
	SafeRejectionConfirmed = "safe.rejection_confirmed"
	SafeRejectionApproved  = "safe.rejection_approved"
	MessagingStream        = "MESSAGING"
	MessagingSubjectPrefix = MessagingStream + "."
)

type (
	// MessagingEvent is the body clc-app-notifications consumes from the
	// MESSAGING stream (clc-app docs/MESSAGING.md, "Event contract").
	//
	// ConversationID is 0 when there is no conversation, e.g. a block between
	// two people who never had one. MessageID is set only on message.created.
	MessagingEvent struct {
		EventID          string                 `json:"eventId"`
		Kind             string                 `json:"kind"`
		OccurredAt       string                 `json:"occurredAt"`
		ConversationID   int64                  `json:"conversationId"`
		ConversationKind string                 `json:"conversationKind"`
		MessageID        *int64                 `json:"messageId,omitempty"`
		Actor            MessagingActor         `json:"actor"`
		Audience         []MessagingAudience    `json:"audience"`
		Resource         *MessagingResource     `json:"resource,omitempty"`
		Resources        []MessagingResource    `json:"resources,omitempty"`
		SafeApproval     *MessagingSafeApproval `json:"safeApproval,omitempty"`
	}

	MessagingActor struct {
		UserID  int64  `json:"userId"`
		Address string `json:"address"`
	}

	// MessagingAudience is a user whose devices must refresh. Push marks the
	// ones who get a notification rather than a silent sync.
	MessagingAudience struct {
		UserID  int64  `json:"userId"`
		Address string `json:"address"`
		Push    bool   `json:"push"`
	}

	MessagingResource struct {
		Kind           string `json:"kind"`
		Address        string `json:"address"`
		Name           string `json:"name"`
		VoucherAddress string `json:"voucherAddress,omitempty"`
		VoucherName    string `json:"voucherName,omitempty"`
	}

	MessagingSafeApproval struct {
		SafeAddress         string   `json:"safeAddress"`
		SafeTxHash          string   `json:"safeTxHash"`
		Stage               string   `json:"stage"`
		Approvals           int      `json:"approvals"`
		ApprovalsRequired   int      `json:"approvalsRequired"`
		Proposed            bool     `json:"proposed"`
		TransactionKind     string   `json:"transactionKind"`
		Action              string   `json:"action,omitempty"`
		RejectsSafeTxHashes []string `json:"rejectsSafeTxHashes,omitempty"`
	}
)
