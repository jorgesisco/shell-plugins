package provision

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"path/filepath"
	"text/template"

	"github.com/1Password/shell-plugins/sdk"
)

// FileProvisioner provisions one or more secrets as a temporary file.
type FileProvisioner struct {
	sdk.Provisioner

	fileContents        FileContentsFunc
	outfileName         string
	outpathFixed        string
	outpathEnvVar       string
	outdirEnvVar        string
	setOutpathAsArg     bool
	outpathArgTemplates []string
}

type FileContentsFunc func(in sdk.ProvisionInput, out *sdk.ProvisionOutput) ([]byte, error)

// FieldAsFile can be used to store the value of a single field as a file.
func FieldAsFile(fieldName sdk.FieldName) FileContentsFunc {
	return FileContentsFunc(func(in sdk.ProvisionInput, _ *sdk.ProvisionOutput) ([]byte, error) {
		if value, ok := in.ItemFields[fieldName]; ok {
			return []byte(value), nil
		} else {
			return nil, fmt.Errorf("no value present in the item for field '%s'", fieldName)
		}
	})
}

// TempFile returns a file provisioner and takes a function that maps a 1Password item to the contents of
// a single file.
func TempFile(fileContents FileContentsFunc, opts ...FileOption) sdk.Provisioner {
	p := FileProvisioner{
		fileContents: fileContents,
	}
	for _, opt := range opts {
		opt(&p)
	}
	return p
}

// FileOption can be used to influence the behavior of the file provisioner.
type FileOption func(*FileProvisioner)

// AtFixedPath can be used to tell the file provisioner to store the credential at a specific location, instead of
// an autogenerated temp dir. This is useful for executables that can only load credentials from a specific path.
func AtFixedPath(path string) FileOption {
	return func(p *FileProvisioner) {
		p.outpathFixed = path
	}
}

// Filename can be used to tell the file provisioner to store the credential with a specific name, instead of
// an autogenerated name. The specified filename will be appended to the path of the autogenerated temp dir.
// Gets ignored if the provision.AtFixedPath option is also set.
func Filename(name string) FileOption {
	return func(p *FileProvisioner) {
		p.outfileName = name
	}
}

// SetPathAsEnvVar can be used to provision the temporary file path as an environment variable.
func SetPathAsEnvVar(envVarName string) FileOption {
	return func(p *FileProvisioner) {
		p.outpathEnvVar = envVarName
	}
}

// SetOutputDirAsEnvVar can be used to provision the directory of the output file as an environment variable.
func SetOutputDirAsEnvVar(envVarName string) FileOption {
	return func(p *FileProvisioner) {
		p.outdirEnvVar = envVarName
	}
}

// AddArgs can be used to add args to the command line. This is useful when the output file path
// should be passed as an arg. The output path is available as "{{ .Path }}" in each arg.
// For example:
// * `AddArgs("--config-file", "{{ .Path }}")` will result in `--config-file /path/to/tempfile`.
// * `AddArgs("--config-file={{ .Path }}")` will result in `--config-file=/path/to/tempfile`.
func AddArgs(argTemplates ...string) FileOption {
	return func(p *FileProvisioner) {
		p.setOutpathAsArg = true
		p.outpathArgTemplates = argTemplates
	}
}

func (p FileProvisioner) Provision(ctx context.Context, in sdk.ProvisionInput, out *sdk.ProvisionOutput) {
	contents, err := p.fileContents(in, out)
	if err != nil {
		out.AddError(err)
		return
	}

	outpath := ""
	if p.outpathFixed != "" {
		// Default to the provision.AtFixedPath option
		outpath = p.outpathFixed
	} else if p.outfileName != "" {
		// Fall back to the provision.Filename option
		outpath = in.FromTempDir(p.outfileName)
	} else {
		// If both are undefined, resort to generating a random filename
		fileName, err := randomFilename()
		if err != nil {
			// This should only fail in rare circumstances
			out.AddError(fmt.Errorf("generating random file name: %s", err))
			return
		}
		outpath = in.FromTempDir(fileName)
	}

	out.AddSecretFile(outpath, contents)

	if p.outpathEnvVar != "" {
		// Populate the specified environment variable with the output path.
		out.AddEnvVar(p.outpathEnvVar, outpath)
	}

	if p.outdirEnvVar != "" {
		// Populate the specified environment variable with the output dir.
		dir := filepath.Dir(outpath)
		out.AddEnvVar(p.outpathEnvVar, dir)
	}

	// Add args to specify the output path.
	if p.setOutpathAsArg {
		tmplData := struct{ Path string }{
			Path: outpath,
		}

		// Resolve arg templates with the resulting output path injected.
		// Example: "--config-file={{ .Path }}" => "--config-file=/tmp/file"
		argsResolved := make([]string, len(p.outpathArgTemplates))
		for i, tmplStr := range p.outpathArgTemplates {
			tmpl, err := template.New("arg").Parse(tmplStr)
			if err != nil {
				out.AddError(err)
				return
			}

			var result bytes.Buffer
			err = tmpl.Execute(&result, tmplData)
			if err != nil {
				out.AddError(err)
				return
			}

			argsResolved[i] = result.String()
		}

		out.AddArgs(argsResolved...)
	}
}

func (p FileProvisioner) Deprovision(ctx context.Context, in sdk.DeprovisionInput, out *sdk.DeprovisionOutput) {
	// Nothing to do here: deleting the files gets taken care of.
}

func (p FileProvisioner) Description() string {
	return "Provision secret file"
}

func randomFilename() (string, error) {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}
