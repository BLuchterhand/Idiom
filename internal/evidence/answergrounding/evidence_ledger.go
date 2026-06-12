package answergrounding

type EvidenceLedger struct {
	receipts   []EvidenceReceipt
	normalizer TargetNormalizer
}

func NewEvidenceLedger(receipts []EvidenceReceipt) EvidenceLedger {
	return EvidenceLedger{
		receipts:   receipts,
		normalizer: TargetNormalizer{},
	}
}

func (l EvidenceLedger) FindAny(kinds ...EvidenceKind) (EvidenceReceipt, bool) {
	for _, receipt := range l.receipts {
		if evidenceKindAllowed(receipt.Kind, kinds) {
			return receipt, true
		}
	}

	return EvidenceReceipt{}, false
}

func (l EvidenceLedger) FindTarget(target string, kinds ...EvidenceKind) (EvidenceReceipt, bool) {
	for _, receipt := range l.receipts {
		if !evidenceKindAllowed(receipt.Kind, kinds) {
			continue
		}

		if l.normalizer.Same(receipt.Target, target) {
			return receipt, true
		}
	}

	return EvidenceReceipt{}, false
}

func (l EvidenceLedger) FindCoveredTarget(target string, startLine int, endLine int, kinds ...EvidenceKind) (EvidenceReceipt, bool) {
	for _, receipt := range l.receipts {
		if !evidenceKindAllowed(receipt.Kind, kinds) {
			continue
		}

		if !l.normalizer.Same(receipt.Target, target) {
			continue
		}

		if receipt.StartLine <= startLine && endLine <= receipt.EndLine {
			return receipt, true
		}
	}

	return EvidenceReceipt{}, false
}

func evidenceKindAllowed(kind EvidenceKind, allowed []EvidenceKind) bool {
	for _, candidate := range allowed {
		if kind == candidate {
			return true
		}
	}

	return false
}
