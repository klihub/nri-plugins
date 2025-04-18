//go:build ignore
// +build ignore

package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path"
	"strings"
	"text/template"

	"k8s.io/klog/v2"
)

type option struct {
	Flag string
	Name string
	Kind string
	Tags string
}

func collectOptions() []*option {
	var (
		flags = flag.NewFlagSet("klog flags", flag.ContinueOnError)
		opts  = []*option{}
	)

	klog.InitFlags(flags)
	flags.SetOutput(ioutil.Discard)
	flags.VisitAll(func(f *flag.Flag) {
		kind := "string"
		if getter, ok := f.Value.(flag.Getter); ok {
			kind = fmt.Sprintf("%T", getter.Get())
			switch kind {
			case "severity.Severity":
				kind = "string"
			case "klog.Level":
				kind = "int"
			case "<nil>":
				kind = "string"
			}
		}
		opts = append(opts, &option{
			Flag: f.Name,
			Name: strings.ToUpper(f.Name[0:1]) + f.Name[1:],
			Kind: kind,
			Tags: fmt.Sprintf("`json:\"%s,omitempty\"`", f.Name),
		})
	})

	return opts
}

func main() {
	if len(os.Args) != 4 {
		log.Fatal(fmt.Errorf("usage: %s package-name struct-type go-file-name", os.Args[0]))
	}

	var (
		pkgName    = os.Args[1]
		structType = os.Args[2]
		fileName   = os.Args[3]
		tmpName    = fileName + ".tmp"
	)

	f, err := os.OpenFile(tmpName, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		f.Close()
		os.Rename(tmpName, fileName)
	}()

	fileTemplate.Execute(f, struct {
		Command string
		Package string
		Struct  string
		Options []*option
	}{
		Command: strings.Join(append([]string{path.Base(os.Args[0])}, os.Args[1:]...), " "),
		Package: pkgName,
		Struct:  structType,
		Options: collectOptions(),
	})
}

var fileTemplate = template.Must(template.New("").Parse(`// Code generated by go generate; DO NOT EDIT.
// generated by: {{ .Command }}

package {{ .Package }}

import (
    "fmt"
)

// +kubebuilder:object:generate=true
type {{ .Struct }} struct {
{{- range .Options }}
    // +optional
    {{ printf "%s" .Name }} *{{ printf "%s" .Kind }} {{ printf "%s" .Tags }}
{{- end }}
}

func (f *{{ .Struct }}) GetByFlag(name string) (string, bool) {
    switch name {
    {{- range .Options }}
    case "{{ printf "%s" .Flag }}":
        if ptr := f.{{ printf "%s" .Name }}; ptr != nil {
            return fmt.Sprintf("%v", *ptr), true
        }
    {{- end }}
    }
    return "", false
}
`))
