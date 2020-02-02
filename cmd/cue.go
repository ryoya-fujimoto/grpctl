package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"cuelang.org/go/cue/format"
	"cuelang.org/go/encoding/protobuf"

	"github.com/emicklei/proto"

	"cuelang.org/go/cue"
	"github.com/mattn/go-zglob"
)

var r cue.Runtime

var wellKnowns = map[string]string{
	"google/protobuf/timestamp.proto": "https://raw.githubusercontent.com/protocolbuffers/protobuf/master/src/google/protobuf/timestamp.proto",
}
var wellKnownRoot = "./tmp/wellknowns"

type testCase struct {
	Name       string
	Method     string
	Proto      []string
	ImportPath []string `json:"import_path"`
	Headers    map[string]string
	Input      json.RawMessage
	Output     json.RawMessage
}

func generateCUEModule(protoRoot string, globs []string) ([]string, []*cue.Instance, error) {
	protoFiles := []string{}
	for _, glob := range globs {
		pFiles, err := zglob.Glob(glob)
		if err != nil {
			return nil, nil, err
		}
		protoFiles = append(protoFiles, pFiles...)
	}

	if len(protoFiles) == 0 {
		return nil, nil, fmt.Errorf("no protofiles")
	}

	err := downloadWellKnowns()
	if err != nil {
		return nil, nil, err
	}

	cueImports := []string{}
	for _, protoFile := range protoFiles {
		pkg, _, err := extractProto(protoFile)
		if err != nil {
			return nil, nil, err
		}
		cueImports = append(cueImports, strings.ReplaceAll(pkg, ";", ":"))
	}

	moduleName := ""
	if len(cueImports) > 0 {
		mp := strings.Split(cueImports[0], "/")[:2]
		moduleName = filepath.Join(mp...)
		err := generateModuleFiles(moduleName)
		if err != nil {
			return nil, nil, err
		}
	}

	instances := []*cue.Instance{}
	// generate cue files
	for _, protoFile := range protoFiles {
		ins, err := generateCUEFile(moduleName, protoRoot, protoFile)
		if err != nil {
			return nil, nil, err
		}
		if ins != nil {
			instances = append(instances, ins)
		}
	}

	return cueImports, instances, nil
}

func generateCUEFile(moduleName, protoRoot, protoFile string) (*cue.Instance, error) {
	p := protoFile
	if !strings.HasPrefix(p, protoRoot) {
		p = filepath.Join(protoRoot, protoFile)
	}
	fmt.Printf("generate cue file from: %s\n", p)

	pkg, imports, err := extractProto(p)
	if err != nil {
		return nil, err
	}

	for _, imp := range imports {
		_, ok := wellKnowns[imp]
		if ok {
			continue
		}

		_, err := generateCUEFile(moduleName, protoRoot, imp)
		if err != nil {
			return nil, err
		}
	}

	cueFile, err := protobuf.Extract(p, nil, &protobuf.Config{
		Paths: []string{protoRoot, wellKnownRoot},
	})
	if err != nil {
		return nil, err
	}

	if pkg == "" {
		result, err := r.CompileFile(cueFile)
		if err != nil {
			return nil, err
		}
		return result, nil
	}

	outDir := strings.ReplaceAll(strings.Split(pkg, ";")[0], moduleName+"/", "")
	err = os.MkdirAll(outDir, 0755)
	if err != nil {
		return nil, err
	}

	outPath := filepath.Join(outDir, filepath.Base(cueFile.Filename))
	outFile, err := os.Create(outPath)
	if err != nil {
		return nil, err
	}
	defer outFile.Close()

	b, err := format.Node(cueFile)
	if err != nil {
		return nil, err
	}

	_, err = outFile.Write(b)
	if err != nil {
		return nil, err
	}

	return nil, nil
}

func generateModuleFiles(moduleName string) error {
	err := os.MkdirAll("./cue.mod/pkg", 0755)
	if err != nil {
		return err
	}
	err = os.MkdirAll("./cue.mod/usr", 0755)
	if err != nil {
		return err
	}

	p, err := os.Create("./cue.mod/module.cue")
	if err != nil {
		return err
	}
	defer p.Close()

	_, err = p.WriteString(fmt.Sprintf("module: \"%s\"", moduleName))
	return err
}

func extractProto(filePath string) (pkgName string, imports []string, err error) {
	r, err := os.Open(filePath)
	if err != nil {
		return "", nil, err
	}
	defer r.Close()

	parser := proto.NewParser(r)
	def, err := parser.Parse()
	if err != nil {
		return "", nil, err
	}

	for _, e := range def.Elements {
		switch x := e.(type) {
		case *proto.Option:
			pkgName = x.Constant.Source
		case *proto.Import:
			imports = append(imports, x.Filename)
		}
	}

	return pkgName, imports, nil
}

func readCueInstance(filename string) (*cue.Instance, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return r.Compile(filename, file)
}

func downloadWellKnowns() error {
	targets := map[string]string{}
	for key, url := range wellKnowns {
		_, err := os.Stat(filepath.Join(wellKnownRoot, key))
		if err != nil {
			targets[key] = url
		}
	}

	if len(targets) == 0 {
		return nil
	}
	fmt.Println("download well-known types")
	for key, url := range targets {
		p := filepath.Join(wellKnownRoot, key)
		fmt.Printf("download %s from %s\n", key, url)

		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			return err
		}

		if err := downloadFile(url, p); err != nil {
			return err
		}
	}

	return nil
}

func downloadFile(url string, filepath string) error {
	// Create the file
	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	// Get the data
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Write the body to file
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return err
	}

	return nil
}
