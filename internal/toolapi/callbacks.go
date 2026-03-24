package toolapi

type Callbacks struct {
	SendMail           func(from, to, subject, body, priority string) error
	ResolveSessionName func(target string) (string, error)
	QueueNudge         func(sessionName, sender, message string) error
	ReportToolError    func(message string)
}
