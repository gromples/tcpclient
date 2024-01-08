package tcpclient

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"
)

// Client represents a TCP client
type Client struct {
	connection    net.Conn
	Name          string
	serverAddr    string
	connectionType string
	requestsMutex  sync.Mutex
	sendMutex  sync.Mutex
	responseChannel       map[string]chan map[string]interface{}
	creationTimes  map[string]time.Time
}

const (
	TypeClient = "1"
	TypeWorker = "2"
)

var counter uint32
var newUniqueMID int64 = 0
var mu sync.Mutex

func init() {
    fmt.Println("\nTCP CLIENT V1.0.1")
	newUniqueMID = time.Now().UnixNano()
}

func uniqueMID() int64 {
    mu.Lock()
    defer mu.Unlock()
	newUniqueMID++

    return newUniqueMID
}


// NewClient creates a new TCP client with a provided response channel
//func NewClient(name, connectionType, serverAddr string, responseChan chan map[string]interface{}) (*Client, error) {
func NewClient(name,serverAddr string) (*Client, error) {
		conn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		return nil, fmt.Errorf("error connecting to server: %s", err)
	}

	// Send the client name to the server
	clientNameMessage := map[string]interface{}{"ServiceName": name, "Type": TypeClient}
	initialMessage, _ := json.Marshal(clientNameMessage)
	conn.Write(initialMessage)

	// Initialize channels and maps
	responseChannel := make(map[string]chan map[string]interface{})
//	responses := make(chan map[string]interface{})
	creationTimes := make(map[string]time.Time)
	// Create the client instance
	c := &Client{
		connection:    conn,
		Name:          name,
		serverAddr:    serverAddr,
		connectionType: TypeClient,
		requestsMutex: sync.Mutex{},
		responseChannel:      responseChannel,
		creationTimes: creationTimes,
	}

	// Start a goroutine to listen for responses from the server
	if err := c.handleResponses(); err != nil {
		return nil, fmt.Errorf("error starting response handling: %s", err)
	}

	// Start a background goroutine to periodically clean up old responses
	if err := c.startCleanupRoutine(); err != nil {
		return nil, fmt.Errorf("error starting cleanup routine: %s", err)
	}

	return c, nil
}

// Reconnect attempts to reconnect the client to the server
func (c *Client) reconnect() error {
	//Make sure the connection is closed
	c.connection.Close()
	conn, err := net.Dial("tcp", c.serverAddr)
	if err != nil {
		return fmt.Errorf("error reconnecting to server: %s", err)
	}

	// Send the client name to the server
	clientNameMessage := map[string]interface{}{"ServiceName": c.Name, "Type": c.connectionType}
	initialMessage, _ := json.Marshal(clientNameMessage)
	conn.Write(initialMessage)

	// Update the connection in the Client struct
	c.connection = conn

	return nil
}

// sendMessage sends a JSON message to the server
func (c *Client) sendMessage(message map[string]interface{}) error {
	c.sendMutex.Lock()
	defer c.sendMutex.Unlock()	
	//fmt.Printf("Starting send of:%v\n",message)
	// Encode the message as JSON
	jsonMessage, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("error encoding JSON: %s", err)
	}

	//fmt.Println("Write to socket")
	// Send the JSON message to the server
	_, err =c.connection.Write(jsonMessage)
	if err != nil {
		return fmt.Errorf("error sending: %s", err)
	}
	return nil
}

// Request sends a request to the server and waits for a response
func (c *Client) SendRequestWait(requestMessage map[string]interface{}, timeOutMilliSecond int) (map[string]interface{}, error) {
	// Generate a unique identifier for the request
	requestID := fmt.Sprintf("%d", uniqueMID())
	//fmt.Println("New Unique ID:",requestID)

	// Create a channel for the response
	responseChan := make(chan map[string]interface{})

	// Register the channel in the responseChannel map
	c.registerRequest(requestID, responseChan)

	// Add the request ID to the message
	requestMessage["MID"] = requestID

	//startTime := time.Now()
	// Send the request to the server
	err := c.sendMessage(requestMessage)
	//elapsedTime := time.Since(startTime)
	//fmt.Printf("Time taken on send: %s\n", elapsedTime)

	if err != nil {
		// Unregister the request on error
		c.unregisterRequest(requestID)
		return nil, err
	}

	//startTime = time.Now()
	// Wait for the response
	select {
	case response := <-responseChan:
		// Response received, unregister the channel
		c.unregisterRequest(requestID)
		//fmt.Println("Received message in SendRequestWait")
		//elapsedTime := time.Since(startTime)
		//fmt.Printf("Time taken on receive: %s\n", elapsedTime)
		return response, nil
	case <-time.After(time.Duration(timeOutMilliSecond) * time.Millisecond): // Adjust the timeout as needed
		// Timeout occurred, unregister the channel and return an error
		c.unregisterRequest(requestID)
		//elapsedTime := time.Since(startTime)
		//fmt.Printf("Time taken on receive: %s\n", elapsedTime)
		return nil, fmt.Errorf("timeout waiting for response")
	}
}

// SendRequestNoWait sends a request to the server without waiting for a response
func (c *Client) SendRequestNoWait(requestMessage map[string]interface{}) (string, error) {
	// Generate a unique identifier for the request
	requestID := fmt.Sprintf("%d", uniqueMID())
	//fmt.Println("New Unique ID:",requestID)

	// Create a channel for the response
	responseChan := make(chan map[string]interface{},10)

	// Register the channel in the responseChannel map
	c.registerRequest(requestID, responseChan)

	// Add the request ID to the message
	requestMessage["MID"] = requestID

	// Send the request to the server
	err := c.sendMessage(requestMessage)
	if err != nil {
		// Unregister the request on error
		c.unregisterRequest(requestID)
		return "", err
	}
	return requestID, nil
}

// CheckResponse checks if a response has been received for a previously sent request
func (c *Client) CheckResponse(requestID string, timeOutMilliSecond int) (map[string]interface{}, error) {
	// Retrieve the response channel associated with the request ID
	responseChan, found := c.getResponseChan(requestID)
	if !found {
		return nil, fmt.Errorf("response not found for request ID: %s", requestID)
	}

	// Check if a response channel is associated with the request
	if responseChan != nil {
		// Wait for the response
		select {
		case response := <-responseChan:
			// Response received, unregister the channel
			c.unregisterRequest(requestID)
			return response, nil
		case <-time.After(time.Duration(timeOutMilliSecond) * time.Millisecond): // Adjust the timeout as needed
			// Timeout occurred, unregister the channel and return an error
			c.unregisterRequest(requestID)
			return nil, fmt.Errorf("timeout waiting for response for request ID: %s", requestID)
		}
	}

	// Return an error indicating that the response channel is not expected
	return nil, fmt.Errorf("response channel not expected for request ID: %s", requestID)
}

// Function to register a request in the responseChannel map
func (c *Client) registerRequest(requestID string, responseChan chan map[string]interface{}) {
	//fmt.Printf("\nClient Register:%v\n",requestID)
	c.requestsMutex.Lock()
	defer c.requestsMutex.Unlock()
	c.responseChannel[requestID] = responseChan
	c.creationTimes[requestID] = time.Now()
}

// Function to unregister a request in the responseChannel map
func (c *Client) unregisterRequest(requestID string) {
	//fmt.Printf("\nClient unRegister:%v\n",requestID)
	c.requestsMutex.Lock()
	defer c.requestsMutex.Unlock()
	//close(c.responseChannel[requestID])  // Close the channel
	delete(c.responseChannel, requestID)
	delete(c.creationTimes, requestID)
	
}

// Function to get the response channel associated with a request ID
func (c *Client) getResponseChan(requestID string) (chan map[string]interface{}, bool) {
	c.requestsMutex.Lock()
	defer c.requestsMutex.Unlock()
	responseChan, found := c.responseChannel[requestID]
	return responseChan, found
}

// Function to handle responses from the server
func (c *Client) handleResponses() error {
	connectionEstablished := true

	go func() {
		for {
			if connectionEstablished {
				decoder := json.NewDecoder(c.connection)
				//fmt.Println("Received message on TCP/IP")
				var responseMessage map[string]interface{}
				//fmt.Println("Before decoder")
				if err := decoder.Decode(&responseMessage); err != nil {
					fmt.Printf("TCP/IP Decoder error:%s\n",err.Error())
					c.connection.Close()
					connectionEstablished = false
				} else {
					//fmt.Printf("Trying to find MID response:%v\n",responseMessage)
					value, found := responseMessage["MID"]
					if found {
						valueStr := value.(string)
						responseChan, found := c.getResponseChan(valueStr)
						if found {
							select {
							case responseChan <- responseMessage:
								;// At least one statement required in a case of a select -> ; is a no-op statement
								//fmt.Println("Received message on channel.",valueStr)
							default:
								fmt.Println("Channel closed. Unable to send.")
							}
						}else{
							fmt.Println("Response Channel not found:",valueStr)
							fmt.Printf("\nThe structure:%v\n",c)
						}
					} 

				}
			} else {
				// Reconnect here using the receiver 'c'
				err := c.reconnect()
				if err != nil {
					fmt.Printf("Error reconnecting to server: %s\n", err)
					fmt.Println("Start sleeping")
					time.Sleep(10 * time.Second)
					fmt.Println("Wake up!")
				} else {
					connectionEstablished = true
				}
			}
		}	
	}()

	return nil
}

// Add a background goroutine to periodically clean up old responses
func (c *Client) startCleanupRoutine() error {
	go func() {
		for {
			time.Sleep(1 * time.Minute) // Adjust the cleanup interval as needed

			// Lock the responseChannel map for reading
			c.requestsMutex.Lock()

			// Get the current time
			currentTime := time.Now()

			// Iterate over the responseChannel map and remove old responses
			for requestID, responseChan := range c.responseChannel {
				if responseChan == nil {
					// Skip responseChannel without a response channel
					continue
				}

				// Check the age of the response
				if creationTime, ok := c.creationTimes[requestID]; ok {
					age := currentTime.Sub(creationTime)
					if age > 2 * time.Minute{
						// Close the response channel to signal that it's no longer needed
						close(responseChan)
						// Remove the entry from the map
						delete(c.responseChannel, requestID)
						delete(c.creationTimes, requestID)
					}
				}
			}

			// Unlock the responseChannel map
			c.requestsMutex.Unlock()
		}
	}()
	return nil
}

func (c *Client) CleanupAfterUse() {
	c.requestsMutex.Lock()
	defer c.requestsMutex.Unlock()

	for requestID, responseChan := range c.responseChannel {
		// Close the response channel to signal that it's no longer needed
		if responseChan != nil {
			close(responseChan)
		}
		// Remove the entry from the map
		delete(c.responseChannel, requestID)
		delete(c.creationTimes, requestID)
	}
	c.connection.Close()
}