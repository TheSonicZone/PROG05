package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"github.com/matishsiao/goInfo"
	"go.bug.st/serial"
	"os"
	"strings"
	"time"
)

// Struct for settings read from the config.json file
// ----------------------------------------------------
type Settings struct {
	Port        string
	Targetclock string
}

// Main Variables
var rambuffer = make([]byte, 255) // Main RAM buffer (1:1) correspondence with HC05 RAM in bootloader mode
var length byte = 1               // Length indicator sen to the bootloader, the count includes itself hence we set it to 1
var selector = 0

var rxbuffer = make([]byte, 1024) // Main serial reception buffer
var rxbuffercount = 0
var tmpbuf = make([]byte, 100)
var port serial.Port

// Test program to be written and run on the HC05 to confirm the board/CPU/serial connection/power is well
// This is the object code compiled with CASM05Z and the S19 record converted directly to binary

// This testprog flashed the LEDs nicely on PORT A
//S1130051 A6 55 B7 00 A6 FF B7 04 A6 AA B7 00 CD 00 69 A6
//S1130061 55 B7 00 CD 00 69 20 F0 A6 55 AE A6 5A 26 FD 4A
//S1060071 26 F8 81

//var testprog = []byte{0xA6, 0x55, 0xB7, 0x00, 0xA6, 0xFF, 0xB7, 0x04, 0xA6, 0xAA, 0xB7, 0x00, 0xCD, 0x00, 0x69, 0xA6,
//	0x55, 0xB7, 0x00, 0xCD, 0x00, 0x69, 0x20, 0xF0, 0xA6, 0xFF, 0xAE, 0xA6, 0x5A, 0x26, 0xFD, 0x4A,
//	0x26, 0xF8, 0x81}

//A6 55 B7 00 A6 FF B7 04 AE 04 3F 0E A6 0C B7 0F
//42 B7 0D A6 AA B7 00 CD 00 93 A6 55 B7 00 CD 00
//93 A6 48 CD 00 8D A6 43 CD 00 8D A6 30 CD 00 8D
//A6 35 CD 00 8D A6 0A CD 00 8D 20 D7 0F 10 FD B7
//11 81 A6 FF AE A6 5A 26 FD 4A 26 F8 81

var testprog = []byte{
	0xA6, 0x55, 0xB7, 0x00, 0xA6, 0xFF, 0xB7, 0x04, 0xAE, 0x04, 0x3F, 0x0E, 0xA6, 0x0C, 0xB7, 0x0F,
	0x42, 0xB7, 0x0D, 0xA6, 0xAA, 0xB7, 0x00, 0xCD, 0x00, 0x93, 0xA6, 0x55, 0xB7, 0x00, 0xCD, 0x00,
	0x93, 0xA6, 0x48, 0xCD, 0x00, 0x8D, 0xA6, 0x43, 0xCD, 0x00, 0x8D, 0xA6, 0x30, 0xCD, 0x00, 0x8D,
	0xA6, 0x35, 0xCD, 0x00, 0x8D, 0xA6, 0x0D, 0xCD, 0x00, 0x8D, 0x20, 0xD7, 0x0F, 0x10, 0xFD, 0xB7,
	0x11, 0x81, 0xA6, 0xFF, 0xAE, 0xA6, 0x5A, 0x26, 0xFD, 0x4A, 0x26, 0xF8, 0x81}

//-------------------------------------------------------------------------------------------------------------------
// Utility Functions
//-------------------------------------------------------------------------------------------------------------------

// Name: LoadSrec
// Function: Load Motorola S-Record file from the disk and parse it, then decode all the data to binary and store
//
//	that in the appropriate buffer
//
// Parameters: Full path to the file that shall be opened, Loading options
// Returns: Result of operation
// ----------------------------------------------------------------------------------------------------------------
func LoadSrec(pathtofile string, loadoption uint8) int {
	return 0
}

func main() {
	fmt.Println("                                              ")
	fmt.Println("╔════════════════════════════════════════════╗")
	fmt.Println("║   PROG05 - A modern 68HC705C8 Programmer   ║")
	fmt.Println("║              Version 1.00 - Sonic2k        ║")
	fmt.Println("╚════════════════════════════════════════════╝")
	fmt.Println("                                             ")

	// Open the config.json file and see what port is specified for use to talk to the hardware
	content, err := os.ReadFile("./config.json")
	if err != nil {
		fmt.Println("Unable to open configuration file: ", err)
	}

	// Print OS information here...
	gi, _ := goInfo.GetInfo()
	fmt.Printf("  OS: %s  VER: %s \r\n\r\n", gi.GoOS, gi.Core)

	var workingset Settings
	err = json.Unmarshal(content, &workingset)
	if err != nil {
		fmt.Println("Configuration file contains invalid data: ", err)
	}
	var tstr string
	tstr = "Configuration Loaded- Port " + workingset.Port + " is assigned"
	fmt.Println(tstr)
	tstr = "Target clock frequency: " + workingset.Targetclock
	fmt.Println(tstr)

	// Attempt to open port specified in config file
	mode := &serial.Mode{
		BaudRate: 4800, // In the absence of being told otherwise, we assume the CPU is clocked at 2MHz
		Parity:   serial.NoParity,
		DataBits: 8,
		StopBits: serial.OneStopBit,
	}
	// If the higher clock frequency is selected we go for it, otherwise we do the Motorola default of 2MHz
	if strings.Contains(workingset.Targetclock, "4MHz") {
		mode.BaudRate = 9600
	}
	port, err = serial.Open(workingset.Port, mode)
	if err != nil {
		fmt.Println("Error opening serial port. Program will now quit")
		os.Exit(0)

	}

	// Serial port was opened OK... begin interactive mode
	go SerialRx()
	fmt.Println("   ** READY TO PROGRAM TARGET MC68HC705C8  **   ")
	fmt.Println("Available Commands:")
	fmt.Println(" * TEST - Load test program into HC05 and check response (supports official boards and MIDON PROG05 programmer)")
	fmt.Println(" * DEMO - Load simple demonstration program into HC05 that toggles PORT A pins (use this to confirm your MCU is OK)")
	fmt.Println(" * LOAD - Load user application into HC05 RAM and execute (specify a .S19 file")
	fmt.Println(" * QUIT - Quit this program ")

	//--------------------------------------------------------------------------------------
	// User Input Handling
	//--------------------------------------------------------------------------------------
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Printf("\r\n>") // Print initial command prompt
		userinput, _ := reader.ReadString('\n')
		//fmt.Println(userinput) // ONLY RETAINED FOR DEBUGGING, REMOVE IT
		// User input obtained, process it

		// Quit command
		//--------------
		if strings.Contains(userinput, "QUIT") {
			port.Close()
			os.Exit(0)
		}
		// TEST command - Load small app into HC05 and process it's response
		//-------------------------------------------------------------------
		if strings.Contains(userinput, "TEST") {
			// Stream binary executable out on the serial port
			fmt.Printf("Loading test program into target RAM")
			length = byte(len(testprog)) + 1
			//fmt.Printf("Length Indicator (1st byte) = %d\r\n", length)
			selector = 0
			var p = make([]byte, 1)

			// Send length to the bootloader
			p[0] = length
			_, err := port.Write(p[0:])
			if err == nil {
				//fmt.Println("Length sent to bootloader...  ")

				// Send all bytes out on the serial port now...
				for n := 0; n < int(length-1); n++ {
					p[0] = testprog[selector]
					time.Sleep(5 * time.Millisecond)
					_, err := port.Write(p[0:])

					if err != nil {
						fmt.Println("Error writing byte to target.. ")
					} else {
						//fmt.Printf("%02X ", p[0:])
						fmt.Printf(".")
					}
					selector++
				}
				fmt.Println(" DONE!")
				fmt.Printf("Checking target.... ")
				// Clear buffer and pointer
				for i := 0; i < 1024; i++ {
					rxbuffer[i] = 0
				}
				rxbuffercount = 0

				// Allow time for the HC05 to have sent its string to the hos
				time.Sleep(800 * time.Millisecond)
				str1 := string(rxbuffer[:rxbuffercount])
				if strings.Contains(str1, "HC05") {
					fmt.Printf(" [OK]\r\n")
					fmt.Println("Target (68HC705C8) access is Successful")
				} else {
					fmt.Printf(" [FAILED]\r\n")
					fmt.Println("  Check your hardware, clock speed, and confirm HC05 did go into bootloader mode")
				}

			} else {
				fmt.Println("\r\nError writing byte to target.. ")
			}
		}

	}

}

// Serial Port Reception goroutine
// This thread will sit and block on the serial port receive callback in the OS
// If a byte is received, it is stored in the buffer
// ----------------------------------------------------------------------------
func SerialRx() {

	for {
		n, _ := port.Read(tmpbuf)
		if n > 0 {
			// n holds the number of bytes we got in this read, copy to buffer
			for r := 0; r < n; r++ {
				rxbuffer[rxbuffercount] = tmpbuf[r]
				rxbuffercount++
				if rxbuffercount > 1023 {
					rxbuffercount = 1023 // reached end of buffer, discard the data until it is emptied
				}
			}
		}
	}
}
