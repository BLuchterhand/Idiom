package answergrounding

const (
	TargetUnkown       = "unknown"
	TargetVerification = "verification"
	TargetChangedPath  = "changed_path"

	SeverityUnknown  = "unknown"
	SeverityBlocking = "blocking"

	SourceBody    EvidenceKind = "source_body"
	SymbolBody    EvidenceKind = "symbol_body"
	LogWindow     EvidenceKind = "log_window"
	CommandResult EvidenceKind = "command_result"
	DiffWrite     EvidenceKind = "diff_write"
	PathSeen      EvidenceKind = "path_seen"
	Packet        EvidenceKind = "packet"
)
