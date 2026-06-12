package autonomy

import "fmt"

type validationProblems struct {
	messages []string
}

func (p *validationProblems) Add(message string) {
	p.messages = append(p.messages, message)
}

func (p *validationProblems) Addf(format string, args ...any) {
	p.Add(fmt.Sprintf(format, args...))
}

func (p *validationProblems) AddError(err error) {
	if err != nil {
		p.Add(err.Error())
	}
}

func (p validationProblems) Any() bool {
	return len(p.messages) > 0
}

func (p validationProblems) Messages() []string {
	return append([]string(nil), p.messages...)
}
