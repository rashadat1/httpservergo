package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
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

const payLoadSizeError string = " 413 Payload too large\r\nContent-Length: 21\r\nContent-Type: text/plain\r\n\r\n413 Content Too Large"
const badRequestMalformed string = " 400 Bad Request: Malformed Request Error\r\nContent-Length: 34\r\nContent-Type: text/plain\r\n\r\n400 Bad Request: Malformed Request\r\n"
const serverError string = " 500 Internal Server Error\r\nContent-Length: 16\r\nContent-Type: text/plain\r\n\r\n500 Server Error\r\n"

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
	for {
		defer conn.Close()
		reader := bufio.NewReader(conn)
		requestParsedRef, err := parseHttp(reader)
		// all parsing errors return a nil requestParsedRef and an http error message in err
		// in the future keep a map that counts the number of times we get a parsing error from a
		// likely attack and revoke connection and blacklist connections from that IP after a thresh
		if err != nil {
			// malformed request: writing error to client
			_, err = conn.Write([]byte(err.Error()))
			if err != nil {
				log.Println("Error writing bytes to client connection (Error): " + err.Error())
				return
			}
			// return here so we dont dereference a null requestParsedRef pointer
			return
		}
		fmt.Println(requestParsedRef.requestLine)
		response, err := createResponse(requestParsedRef, directory)
		if err != nil {
			// the only time we have a non-nil error is when the endpoint is invalid
			log.Println("Error creating response to request: " + err.Error())
		}
		// otherwise errors from creating a response are encoded in the response variable
		// as a valid http string and simply sent to the client
		responseString, dontKeepAlive := buildResponseString(response)
		log.Println("Response String:\r\n" + responseString)
		_, err = conn.Write([]byte(responseString))
		if err != nil {
			fmt.Println("Error writing responseString to client connection: " + err.Error())
		}
		if dontKeepAlive {
			break
		}

	}
	return
}
func compressBodyGzip(response *Response) (bytes.Buffer, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	// writes a compressed form of response.responseBody to the underlying buf
	_, err := zw.Write([]byte(response.responseBody))
	if err != nil {
		log.Println("Error compressing response body: " + err.Error())
		return buf, errors.New(strings.Split(response.statusLine, " ")[0] + serverError)
	}
	if err := zw.Close(); err != nil {
		log.Fatal(err)
	}
	return buf, nil
}
func buildResponseString(response *Response) (string, bool) {
	var responseString string
	var dontKeepAlive bool = false
	responseString += response.statusLine
	if response.headers["Content-Encoding"] == "gzip" {
		// encode body and set content-length to length of encoded body
		compressedBytesBuffer, err := compressBodyGzip(response)
		if err != nil {
			responseString = err.Error()
			dontKeepAlive = true
			return responseString, dontKeepAlive
		}
		response.responseBody = compressedBytesBuffer.String()
		response.headers["Content-Length"] = fmt.Sprintf("%d", len(response.responseBody))
	}
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
	if strings.Contains(responseString, "Connection: close") {
		dontKeepAlive = true
	}
	if strings.Contains(responseString, "500 Server Error") {
		dontKeepAlive = true
	}
	return responseString, dontKeepAlive
}
func createResponse(requestParsed *Request, directory string) (*Response, error) {
	response := Response{}
	response.headers = make(map[string]string)
	response.statusLine = requestParsed.httpVersion + " 200 OK\r\n"
	if requestParsed.headers["Connection"] == "close" {
		response.headers["Connection"] = "close"
	}
	if strings.Contains(requestParsed.headers["Accept-Encoding"], "gzip") {
		response.headers["Content-Encoding"] = "gzip"
	}
	if requestParsed.urlPath == "/" {
		return &response, nil
	} else if strings.HasPrefix(requestParsed.urlPath, "/echo/") {
		stringEcho := strings.Split(requestParsed.urlPath, "/echo/")[1]

		response.headers["Content-Type"] = "text/plain"
		response.headers["Content-Length"] = fmt.Sprintf("%d", len(stringEcho))
		response.responseBody = stringEcho
		return &response, nil

	} else if strings.HasPrefix(requestParsed.urlPath, "/user-agent") {
		userAgent := requestParsed.headers["User-Agent"]
		response.headers["Content-Type"] = "text/plain"
		response.headers["Content-Length"] = fmt.Sprintf("%d", len(userAgent))
		response.responseBody = userAgent
		return &response, nil

	} else if strings.HasPrefix(requestParsed.urlPath, "/files/") {

		fileName := strings.Split(requestParsed.urlPath, "/files/")[1]
		fullFilePath := filepath.Join(directory, fileName)
		if requestParsed.requestMethod == "GET" {
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
			fmt.Println("POST request received to files endpoint")
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
			err = os.WriteFile(fullFilePath, fileBuffer, 0777)
			fmt.Println("Writing file: " + fullFilePath)
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
		return nil, errors.New("HTTP/1.1" + badRequestMalformed)
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
		return nil, errors.New("HTTP/1.1" + badRequestMalformed)
	}

	request.headers = make(map[string]string)
	var headerCount int
	var totalHeaderSize int
	// catch duplicate content-length Headers
	seenContentLength := false

	for {
		if headerCount > maxNumHeaders || totalHeaderSize > totalHeaderSizeLimit {
			log.Println("Possible attack vector:")
			if headerCount > maxNumHeaders {
				log.Println("Number of headers - " + strconv.Itoa(headerCount) + " - exceeds max header count - " + strconv.Itoa(maxNumHeaders))
			} else {
				log.Println("Total size of header section - " + strconv.Itoa(totalHeaderSize) + " - exceeds header size limit - " + strconv.Itoa(totalHeaderSizeLimit))
			}
			return nil, errors.New(request.httpVersion + badRequestMalformed)
		}
		// stop reading after we have read 512 bytes in a single line and return an error
		headerLine, err := readLimitedLine(reader, maxHeaderLineLength)
		if err != nil {
			return nil, errors.New(request.httpVersion + serverError)
		}
		if headerLine == "" {
			return nil, errors.New(request.httpVersion + badRequestMalformed)
		}
		fmt.Println(headerLine)
		if headerLine == "\r\n" || headerLine == "\n" {
			break
		}

		headerLine = strings.Trim(headerLine, "\r\n ")
		if !validPrintableAsciiHeader(headerLine) {
			log.Println("Error reading headerLine: Not Valid Printable ASCII")
			return nil, errors.New(request.httpVersion + badRequestMalformed)
		}
		headerLineParts := strings.Split(headerLine, ":")
		if len(headerLineParts) != 2 || headerLineParts[0] == "" || headerLineParts[1] == "" {
			if headerLineParts[0] != "Host" {
				// if line in header is not of the form key: value where both key and value are non-empty
				log.Println("Malformed header line: " + headerLine)
				return nil, errors.New(request.httpVersion + badRequestMalformed)
			}
		}
		key := strings.Trim(headerLineParts[0], " ")
		valJoined := strings.Join(headerLineParts[1:], ":")
		val := strings.TrimSpace(valJoined)

		if key == "Content-Length" {
			if seenContentLength {
				log.Println("Possible attack vector - Duplicated Content-Length header")
				return nil, errors.New(request.httpVersion + badRequestMalformed)
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
		return nil, errors.New(request.httpVersion + badRequestMalformed)
	}
	if request.requestMethod == "POST" {
		contentLengthStr := request.headers["Content-Length"]
		if contentLengthStr != "" {
			// "" means it does not exist in the headers map - if we have a POST we require a contentLength header
			contentLength, err := strconv.Atoi(contentLengthStr)
			if err != nil {
				log.Println("Error converting contentLength to int while parsing request: " + err.Error())
				return nil, errors.New(request.httpVersion + serverError)
			}
			if contentLength > maxAllowedBodySize {
				log.Println("Content-Length - " + strconv.Itoa(contentLength) + " - exceeds maximum allowed body size - " + strconv.Itoa(maxAllowedBodySize))
				return nil, errors.New(request.httpVersion + payLoadSizeError)
			}
			bodyBytes := make([]byte, contentLength)
			n, err := io.ReadFull(reader, bodyBytes)
			fmt.Println("Bytes read from body", n)
			if err != nil {
				log.Println("Error reading into buffer parsing request Body: " + err.Error())
				return nil, errors.New(request.httpVersion + serverError)
			}
			request.body = string(bodyBytes)
		} else {
			return nil, errors.New(request.httpVersion + badRequestMalformed)
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
func readLimitedLine(reader *bufio.Reader, maxLineLen int64) (string, error) {
	limited := &io.LimitedReader{R: reader, N: maxLineLen}
	var builder strings.Builder
	buf := make([]byte, 1)

	for {
		n, err := limited.Read(buf)
		if n > 0 {
			builder.WriteByte(buf[0])
			if buf[0] == '\n' {
				break
			}
		}
		if err != nil {
			if err == io.EOF {
				log.Println("Header line too long (truncated):", len(builder.String()), "bytes")
				return "", nil
			}
			log.Println("Error reading headerLine: " + err.Error())
			return "", err
		}
	}
	return builder.String(), nil
}
