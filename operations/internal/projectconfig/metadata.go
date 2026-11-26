// Package projectconfig contains the project metadata shared by source
// inspection and the saved project catalog.
package projectconfig

type EnvHint struct {
	Name         string `json:"name"`
	DefaultValue string `json:"defaultValue,omitempty"`
	Sensitive    bool   `json:"sensitive,omitempty"`
	Profile      string `json:"profile,omitempty"`
}

type ProxyHint struct {
	Location    string `json:"location"`
	StripPrefix bool   `json:"stripPrefix,omitempty"`
	TargetPort  int    `json:"targetPort,omitempty"`
}

type ConfigTemplate struct {
	Filename string `json:"filename"`
	Profile  string `json:"profile,omitempty"`
	Format   string `json:"format,omitempty"`
	Content  string `json:"content"`
}
