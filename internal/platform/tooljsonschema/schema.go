package tooljsonschema

import "encoding/json"

func Object(required []string, properties string) string {
	if properties != "" {
		properties += ","
	}

	properties += `"named_gap":` + StringProperty("optional: after answer-readiness or a child packet, name one exact unresolved claim this follow-up will verify or falsify; make it specific enough that the next probe is obvious") + `,"why_not_answering":` + StringProperty("optional: short reason describing what would be materially wrong or missing if you answered now")

	req := ""
	if len(required) > 0 {
		req = `,"required":[`
		for i, name := range required {
			if i > 0 {
				req += ","
			}
			req += `"` + name + `"`
		}
		req += `]`
	}

	return `{"type":"object","properties":{` + properties + `}` + req + `}`
}

func StringProperty(desc string) string {
	return `{"type":"string","description":"` + desc + `"}`
}

func IntegerProperty(desc string) string {
	return `{"type":"integer","description":"` + desc + `"}`
}

func BooleanProperty(desc string) string {
	return `{"type":"boolean","description":"` + desc + `"}`
}

func StringEnum(values []string) string {
	encoded, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}
