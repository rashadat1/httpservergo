package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Ensures gofmt doesn't remove the "net" and "os" imports above (feel free to remove this!)
var _ = net.Listen
var _ = os.Exit

type Request struct {
	requestLine   string
	requestMethod string
	httpVersion   string
	urlPath       string
	headers       map[string]string
	body          string
}

type Response struct {
	statusLine   string
	headers      map[string]string
	responseBody string
}

func main() {
	args := os.Args
	var directory string
	if len(args) > 1 {
		for i := 1; i < len(args); i += 2 {
			if args[i] == "--directory" {
				directory = args[i+1]
			}
		}
	}
	if directory != "" {
		fmt.Println("Directory specified: " + directory)
	}
	fmt.Println("Server listening on Port 4221")

	ln, err := net.Listen("tcp", "127.0.0.1:4221")
	defer ln.Close()
	if err != nil {
		log.Fatal("Error starting server on port: " + err.Error())
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Println("Error accepting connection: " + err.Error())
			continue
		}
		go handleConnection(conn, directory)
	}
}

func handleConnection(conn net.Conn, directory string) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	requestParsedRef, err := parseHttp(reader)
	if err != nil {
		// malformed request: writing error to client
		_, err = conn.Write([]byte(err.Error()))
		if err != nil {
			fmt.Println("Error writing bytes to client connection (Error): " + err.Error())
			return
		}
		// return here so we dont dereference a null requestParsedRef pointer
		return
	}
	fmt.Println(requestParsedRef.requestLine)
	response, err := createResponse(requestParsedRef, directory)
	if err != nil {
		// invalid endpoint
		fmt.Println("Error creating response to request: " + err.Error())
	}
	responseString := buildResponseString(response)
	log.Println("Response String:\r\n" + responseString)
	_, err = conn.Write([]byte(responseString))
	if err != nil {
		fmt.Println("Error writing responseString to client connection: " + err.Error())
	}
	return
}
func buildResponseString(response *Response) string {
	var responseString string
	responseString += response.statusLine
	if response.headers != nil {
		for key, value := range response.headers {
			responseString += key
			responseString += ": "
			responseString += value
			responseString += "\r\n"
		}
	}
	responseString += "\r\n"
	responseString += response.responseBody
	return responseString
}
func createResponse(requestParsed *Request, directory string) (*Response, error) {
	response := Response{}
	response.statusLine = requestParsed.httpVersion + " 200 OK\r\n"
	if requestParsed.urlPath == "/" {
		return &response, nil
	} else if strings.HasPrefix(requestParsed.urlPath, "/echo/") {
		response.headers = make(map[string]string)
		stringEcho := strings.Split(requestParsed.urlPath, "/echo/")[1]

		response.headers["Content-Type"] = "text/plain"
		response.headers["Content-Length"] = fmt.Sprintf("%d", len(stringEcho))
		response.responseBody = stringEcho
		return &response, nil

	} else if strings.HasPrefix(requestParsed.urlPath, "/user-agent") {
		response.headers = make(map[string]string)
		userAgent := requestParsed.headers["User-Agent"]
		response.headers["Content-Type"] = "text/plain"
		response.headers["Content-Length"] = fmt.Sprintf("%d", len(userAgent))
		response.responseBody = userAgent
		return &response, nil

	} else if strings.HasPrefix(requestParsed.urlPath, "/files/") {
		fileName := strings.Split(requestParsed.urlPath, "/files/")[1]
		fullFilePath := filepath.Join(directory, fileName)
		/*
			unescapedPath, err := url.PathUnescape(fullFilePath)
			if err != nil {
				log.Println("Error processing /files/ endpoint: Error unescaping path - " + fullFilePath)
				response.statusLine = requestParsed.httpVersion + " 404 Not Found\r\n"
				return &response, nil
			}
			cleanedPath := filepath.Clean(unescapedPath) // resolve .., ., and separator elements to their meanings in the file system
			if !strings.HasPrefix(cleanedPath, directory) {
				log.Println("Error processing /files/ endpoint: User submitted filepath outside of root directory: " + unescapedPath)
				response.statusLine = requestParsed.httpVersion + " 404 Not Found\r\n"
				return &response, nil
			}
		*/
		if requestParsed.requestMethod == "GET" {
			response.headers = make(map[string]string)
			fmt.Println("GET Request to Files Endpoint")
			data, err := os.ReadFile(fullFilePath)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					log.Println("File does not exist: ", fullFilePath)
					response.statusLine = requestParsed.httpVersion + " 404 Not Found\r\n"
					response.headers = map[string]string{
						"Content-Type":   "text/plain",
						"Content-Length": "13",
					}
					response.responseBody = "404 Not Found\n"
					return &response, nil
				}
				response.statusLine = requestParsed.httpVersion + " 500 Internal Server Error\r\n"
				log.Println("Error processing /files/ endpoint: " + err.Error())
				return &response, nil
			}
			response.headers["Content-Type"] = "application/octet-stream"
			response.headers["Content-Length"] = fmt.Sprintf("%d", len(data))
			response.responseBody = string(data)
			return &response, nil

		} else if requestParsed.requestMethod == "POST" {
			numBytesToRead, err := strconv.Atoi(requestParsed.headers["Content-Length"])
			if err != nil {
				log.Println("Error reading content-length from header: " + err.Error())
				response.statusLine = requestParsed.httpVersion + " 500 Internal Server Error\r\n"
				response.headers = map[string]string{
					"Content-Type":   "text/plain",
					"Content-Length": "13",
				}
				response.responseBody = "500 Internal Server Error\n"
				return &response, nil
			}
			fileBuffer := make([]byte, numBytesToRead)
			stringReader := strings.NewReader(requestParsed.body)
			_, err = io.ReadFull(stringReader, fileBuffer)
			if err != nil {
				log.Println("Error reading file content into buffer: " + err.Error())
				response.statusLine = requestParsed.httpVersion + " 500 Internal Server Error\r\n"
				response.headers = map[string]string{
					"Content-Type":   "text/plain",
					"Content-Length": "13",
				}
				response.responseBody = "500 Internal Server Error\n"
				return &response, nil
			}
			err = os.WriteFile(directory+fileName, fileBuffer, 0777)
			if err != nil {
				log.Println("Error writing file: " + directory + fileName + " - " + err.Error())
				response.statusLine = requestParsed.httpVersion + " 500 Internal Server Error\r\n"
				response.headers = map[string]string{
					"Content-Type":   "text/plain",
					"Content-Length": "13",
				}
				response.responseBody = "500 Internal Server Error\n"
				return &response, nil
			}
			response.statusLine = "HTTP/1.1 201 Created\r\n"
			return &response, nil
		}
	}
	response.statusLine = requestParsed.httpVersion + " 404 Not Found\r\n"
	response.headers = map[string]string{
		"Content-Type":   "text/plain",
		"Content-Length": "13",
	}
	response.responseBody = "404 Not Found\n"
	return &response, errors.New("Invalid endpoint")
}
func parseHttp(reader *bufio.Reader) (*Request, error) {
	maxNumHeaders := 50
	totalHeaderSizeLimit := 1024
	const maxHeaderLineLength int64 = 512
	const maxAllowedBodySize int = 1_048_576
	request := Request{}
	requestLine, err := reader.ReadString('\n')
	if err != nil {
		log.Println("Error reading requestLine: " + err.Error())
		return nil, errors.New("HTTP/1.1 400 Bad Request\r\n\r\n")
	}
	requestLine = strings.Trim(requestLine, "\r\n ") // trim \r\n as well as empty spaces
	request.requestLine = requestLine
	requestLineParts := strings.Split(requestLine, " ")
	if len(requestLineParts) != 3 {
		return nil, errors.New("HTTP/1.1 400 Bad Request: Malformed Request Error\r\nContent-Length: 34\r\nContent-Type: text/plain\r\n\r\n400 Bad Request: Malformed Request\r\n")
	}
	request.requestMethod = strings.Trim(requestLineParts[0], " \r\n")
	request.urlPath = strings.Trim(requestLineParts[1], " \r\n")
	request.httpVersion = strings.Trim(requestLineParts[2], " \r\n")

	fmt.Println(request.requestMethod)
	fmt.Println(request.urlPath)
	fmt.Println(request.httpVersion)
	// requestLine validation
	fmt.Println("Headers")
	validRequestLine := requestLineValidation(&request)
	if !validRequestLine {
		return nil, errors.New("HTTP/1.1 400 Bad Request: Malformed Request Error\r\nContent-Length: 34\r\nContent-Type: text/plain\r\n\r\n400 Bad Request: Malformed Request\r\n")
	}

	request.headers = make(map[string]string)
	var headerCount int
	var totalHeaderSize int
	// catch duplicate content-length Headers
	seenContentLength := false

	limited := &io.LimitedReader{R: reader, N: maxHeaderLineLength}
	limitedReader := bufio.NewReader(limited)
	for {
		if headerCount > maxNumHeaders || totalHeaderSize > totalHeaderSizeLimit {
			log.Println("Possible attack vector:")
			if headerCount > maxNumHeaders {
				log.Println("Number of headers - " + strconv.Itoa(headerCount) + " - exceeds max header count - " + strconv.Itoa(maxNumHeaders))
			} else {
				log.Println("Total size of header section - " + strconv.Itoa(totalHeaderSize) + " - exceeds header size limit - " + strconv.Itoa(totalHeaderSizeLimit))
			}
			return nil, errors.New("HTTP/1.1 400 Bad Request: Malformed Request Error\r\nContent-Length: 34\r\nContent-Type: text/plain\r\n\r\n400 Bad Request: Malformed Request\r\n")
		}
		// stop reading after we have read 512 bytes in a single line and return an error
		headerLine, err := limitedReader.ReadString('\n')
		if err != nil {
			if err == io.EOF && !strings.HasSuffix(headerLine, "\n") {
				log.Println("Header line too long (truncated):", len(headerLine), "bytes")

			}
			log.Println("Error reading headerLine: " + err.Error())
			return nil, errors.New(request.httpVersion + " 400 Bad Request: Malformed Request Error\r\nContent-Length: 34\r\nContent-Type: text/plain\r\n\r\n400 Bad Request: Malformed Request\r\n")
		}
		fmt.Println("Header Line: " + headerLine)
		if headerLine == "\r\n" || headerLine == "\n" {
			break
		}

		headerLine = strings.Trim(headerLine, "\r\n ")
		if !validPrintableAsciiHeader(headerLine) {
			log.Println("Error reading headerLine: Not Valid Printable ASCII")
			return nil, errors.New(request.httpVersion + " 400 Bad Request: Malformed Request Error\r\nContent-Length: 15\r\nContent-Type: text/plain\r\n\r\n400 Bad Request\r\n")
		}
		headerLineParts := strings.Split(headerLine, ":")
		if len(headerLineParts) != 2 || headerLineParts[0] == "" || headerLineParts[1] == "" {
			if headerLineParts[0] != "Host" {
				// if line in header is not of the form key: value where both key and value are non-empty
				log.Println("Malformed header line: " + headerLine)
				return nil, errors.New(request.httpVersion + " 400 Bad Request: Malformed Request Error\r\nContent-Length: 15\r\nContent-Type: text/plain\r\n\r\n400 Bad Request\r\n")
			}
		}
		key := strings.Trim(headerLineParts[0], " ")
		val := strings.Join(headerLineParts[1:], ":")
		val = strings.TrimSpace(val)

		if key == "Content-Length" {
			if seenContentLength {
				log.Println("Possible attack vector - Duplicated Content-Length header")
				return nil, errors.New(request.httpVersion + " 400 Bad Request: Malformed Request Error\r\nContent-Length: 15\r\nContent-Type: text/plain\r\n\r\n400 Bad Request\r\n")
			} else {
				seenContentLength = true
			}
		}
		request.headers[key] = val

		totalHeaderSize += len(key)
		totalHeaderSize += len(val)
		headerCount += 1
	}
	if request.headers["Host"] == "" {
		// Host missing from headers
		log.Println("Malformed header section: Missing required Host header")
		return nil, errors.New(request.httpVersion + " 400 Bad Request: Malformed Request Error\r\nContent-Length: 15\r\nContent-Type: text/plain\r\n\r\n400 Bad Request\r\n")
	}
	if request.requestMethod == "POST" {
		contentLengthStr := request.headers["Content-Length"]
		if contentLengthStr != "" {
			// "" means it does not exist in the headers map - if we have a POST we require a contentLength header
			contentLength, err := strconv.Atoi(contentLengthStr)
			if err != nil {
				log.Println("Error converting contentLength to int while parsing request: " + err.Error())
				return nil, errors.New(request.httpVersion + " 500 Internal Server Error\r\nContent-Length: 16\r\nContent-Type: text/plain\r\n\r\n500 Server Error\r\n")
			}
			if contentLength > maxAllowedBodySize {
				log.Println("Content-Length - " + strconv.Itoa(contentLength) + " - exceeds maximum allowed body size - " + strconv.Itoa(maxAllowedBodySize))
				return nil, errors.New(request.httpVersion + " 413 Payload too large\r\nContent-Length: 21\r\nContent-Type: text/plain\r\n\r\n413 Content Too Large")
			}
			bodyBytes := make([]byte, contentLength)
			_, err = io.ReadFull(reader, bodyBytes)
			if err != nil {
				log.Println("Error reading into buffer parsing request Body: " + err.Error())
				return nil, errors.New(request.httpVersion + " 500 Internal Server Error\r\nContent-Length: 16\r\nContent-Type: text/plain\r\n\r\n500 Server Error\r\n")
			}
			request.body = string(bodyBytes)
		} else {
			return nil, errors.New(request.httpVersion + " 400 Bad Request: Malformed Request Error\r\nContent-Length: 34\r\nContent-Type: text/plain\r\n\r\n400 Bad Request: Malformed Request\r\n")
		}
	}
	// if the requst is a post or a put we might have a body - in which case we should read exactly
	// (content-length: value) > value number of bytes into a byte array then convert the []byte into a string
	return &request, nil
}
func requestLineValidation(request *Request) bool {
	validHttpVersion, validMethod, validRequestLineLength, validRequestUrl := true, true, true, true
	if request.httpVersion != "HTTP/1.1" {
		// request version validation
		validHttpVersion = false
		log.Println("Invalid HTTP Version: " + request.httpVersion)
	}
	if request.requestMethod != "GET" && request.requestMethod != "POST" {
		// request method validation
		validMethod = false
		log.Println("Invalid HTTP Method: " + request.requestMethod)
	}
	if len(request.requestLine) > 128 {
		// request line length validation
		validRequestLineLength = false
		log.Println("Request Line exceeded max length of 128 bytes - potential attack vector: " + strconv.Itoa(len(request.requestLine)))
	}
	escapedUrl, err := url.PathUnescape(request.urlPath)
	if err != nil {
		log.Println("Invalid urlPath: " + request.urlPath)
		validRequestUrl = false
	}
	if strings.Contains(escapedUrl, "..") ||
		strings.Contains(escapedUrl, "//") ||
		strings.Contains(escapedUrl, "http://") ||
		strings.Contains(escapedUrl, "https://") ||
		strings.Contains(escapedUrl, "\x00") {
		// request url validation

		log.Println("Invalid urlPath: " + request.urlPath)
		validRequestUrl = false
	}
	return validHttpVersion && validMethod && validRequestLineLength && validRequestUrl
}
func validPrintableAsciiHeader(headerLine string) bool {
	for i := range len(headerLine) {
		if headerLine[i] < 32 || headerLine[i] > 126 {
			return false
		}
	}
	return true
}
