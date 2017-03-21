package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	unipdf "github.com/unidoc/unidoc/pdf"
)

func main() {
	const bindAddress = ":3000"
	secure := strings.ToLower(strings.TrimSpace(os.Getenv("SECURE")))
	if secure == "" {
		secure = "true"
	}

	http.HandleFunc("/", requestHandler)
	baseDir := filepath.Dir(os.Args[0])
	var err error
	if secure == "false" {
		fmt.Println("INSECURE http server listening on", bindAddress)
		err = http.ListenAndServe(bindAddress, nil)
	} else {
		fmt.Println("Secure https server listening on", bindAddress)
		err = http.ListenAndServeTLS(bindAddress, filepath.Join(baseDir, "ssl/cert.pem"), filepath.Join(baseDir, "ssl/key.pem"), nil)
	}
	if err != nil {
		log.Panic(err)
	}
}

type batchRequest struct {
	Output   string
	Requests []*documentRequest
}

type documentRequest struct {
	Content string
	Url     string
	// TODO: whitelist options that can be passed to avoid errors,
	// log warning when different options get passed
	Options map[string]interface{}
	Cookies map[string]string
}

func logOutput(request *http.Request, message string) {
	ip := strings.Split(request.RemoteAddr, ":")[0]
	fmt.Println(ip, request.Method, request.URL, message)
}

func requestHandler(response http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/" {
		response.WriteHeader(http.StatusNotFound)
		logOutput(request, "404 not found")
		return
	}
	if request.Method != "POST" {
		response.Header().Set("Allow", "POST")
		response.WriteHeader(http.StatusMethodNotAllowed)
		logOutput(request, "405 not allowed")
		return
	}
	decoder := json.NewDecoder(request.Body)
	var batchRequest batchRequest
	if err := decoder.Decode(&batchRequest); err != nil {
		response.WriteHeader(http.StatusBadRequest)
		logOutput(request, "400 bad request (invalid JSON)")
		return
	}
	if len(batchRequest.Requests) == 0 {
		return
	}

	var programFile string
	var contentType string
	var contentArgs []string
	isPdf := false
	switch batchRequest.Output {
	case "jpg":
		programFile = "/usr/local/bin/wkhtmltoimage"
		contentType = "image/jpeg"
		contentArgs = []string{"--format", "jpg", "-q"}
	case "png":
		programFile = "/usr/local/bin/wkhtmltoimage"
		contentType = "image/png"
		contentArgs = []string{"--format", "png", "-q"}
	default:
		// defaults to pdf
		programFile = "/usr/local/bin/wkhtmltopdf"
		contentType = "application/pdf"
		isPdf = true
	}
	response.Header().Set("Content-Type", contentType)

	if isPdf {
		if len(batchRequest.Requests) == 1 {
			processRequest(batchRequest.Requests[0], programFile, contentArgs, response)
		} else {
			writePdfResponse(batchRequest, programFile, contentArgs, response)
		}
	} else {
		processRequest(batchRequest.Requests[0], programFile, contentArgs, response)
	}

	// TODO: check if Stderr has anything, and issue http 500 instead.
	logOutput(request, "200 OK")
}

func writePdfResponse(request batchRequest, programFile string, contentArgs []string, response http.ResponseWriter) {
	pdfWriter := unipdf.NewPdfWriter()
	output := new(bytes.Buffer)
	for _, documentRequest := range request.Requests {
		output.Reset()
		processRequest(documentRequest, programFile, contentArgs, output)
		pdfReader, err := unipdf.NewPdfReader(bytes.NewReader(output.Bytes()))
		if err != nil {
			fmt.Println(err)
			continue
		}

		numPages, err := pdfReader.GetNumPages()
		if err != nil {
			fmt.Println(err)
			continue
		}

		for i := 0; i < numPages; i++ {
			page, err := pdfReader.GetPage(i + 1)
			if err != nil {
				fmt.Println(err)
				continue
			}

			err = pdfWriter.AddPage(page)
			if err != nil {
				fmt.Println(err)
			}
		}
	}

	tempFile, err := ioutil.TempFile("", "")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer os.Remove(tempFile.Name())
	pdfWriter.Write(tempFile)
	tempFile.Seek(io.SeekStart, 0)
	io.Copy(response, tempFile)
}

func processRequest(req *documentRequest, programFile string, contentArgs []string, output io.Writer) {
	segments := make([]string, 0)
	for key, element := range req.Options {
		if element == true {
			// if it was parsed from the JSON as an actual boolean,
			// convert to command-line single argument	(--foo)
			segments = append(segments, fmt.Sprintf("--%v", key))
		} else if element != false {
			// Otherwise, use command-line argument with value (--foo bar)
			segments = append(segments, fmt.Sprintf("--%v", key), fmt.Sprintf("%v", element))
		}
	}
	for key, value := range req.Cookies {
		segments = append(segments, "--cookie", key, url.QueryEscape(value))
	}

	segments = append(segments, contentArgs...)

	if req.Content != "" {
		segments = append(segments, "-", "-")
	} else {
		segments = append(segments, req.Url, "-")
	}
	fmt.Println("\tRunning:", programFile, strings.Join(segments, " "))

	cmd := exec.Command(programFile, segments...)
	cmd.Stdout = output
	var cmdStdin io.WriteCloser
	if req.Content != "" {
		cmdStdin, _ = cmd.StdinPipe()
	}

	cmd.Start()
	if cmdStdin != nil {
		cmdStdin.Write([]byte(req.Content))
		cmdStdin.Close()
	}
	defer cmd.Wait()
}
