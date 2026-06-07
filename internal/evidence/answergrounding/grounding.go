package answergrounding

type EvidenceKind string

type ClaimKind string

const (
	Path               ClaimKind = "path"
	SourceLocation     ClaimKind = "source_location"
	ClaimCommandResult ClaimKind = "command_result"
	Change             ClaimKind = "change"
	Unsupported        ClaimKind = "unsupported"
)

type EvidenceReceipt struct {
	ID        string
	Kind      EvidenceKind
	Target    string
	StartLine int
	EndLine   int
	Supports  []ClaimKind
	Summary   string
	Fresh     bool
}

type AnswerClaim struct {
	Text        string
	Kind        ClaimKind
	Target      string
	Line        int
	EvidenceIDs []string
	Supported   bool
	Severity    string
	Reason      string
}

type AnswerGroundingResult struct {
	Claims              []AnswerClaim
	BlockingClaims      []AnswerClaim
	HasBlockingFindings bool
}

func Evaluate(answer string, receipts []EvidenceReceipt) AnswerGroundingResult {
	var result AnswerGroundingResult

	extractor := ClaimExtractor{}
	validator := NewGroundingValidator(receipts)
	for _, claim := range validator.Validate(extractor.Extract(answer)) {
		result.Claims = append(result.Claims, claim)
		if claim.Severity == SeverityBlocking {
			result.BlockingClaims = append(result.BlockingClaims, claim)
		}
	}
	result.HasBlockingFindings = len(result.BlockingClaims) > 0
	return result
}
