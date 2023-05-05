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
var length byte = 1               // Length indicator sent to the bootloader, the count includes itself hence we set it to 1
var selector = 0

var rxbuffer = make([]byte, 1024) // Main serial reception buffer
var rxbuffercount = 0
var tmpbuf = make([]byte, 100)
var port serial.Port

const RAM_0050 = 1

// 68HC705C8 memory area images
//------------------------------

var RAM = make([]byte, 176)      // Main RAM + STACK
var USER_PROM = make([]byte, 96) // If RAM1 bit = 0
var RAM2 = make([]byte, 96)      // If RAM1 bit = 1
var PROM = make([]byte, 7584)
var OPTION_REGISTER byte            // Address 0x1FDF
var MASK_OPTION_REGISTER1 byte      // Address 0x1FF0
var MASK_OPTION_REGISTER2 byte      // Address 0x1FF1
var PROM_VECTORS = make([]byte, 12) // 1FF2 - 1FFF

var RAM_SIZE_LOADED uint16 = 0
var RAM_PROGRAM_START uint16 = 0

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

func asciihex2bin(digit1 byte, digit2 byte) byte {
	var returnvalue byte = 0

	if digit1 > 0x2F && digit1 < 0x3A || digit1 > 0x40 && digit1 < 0x47 {
		// Value is between ASCII '0'..'9' and ASCII 'A'..'F'
		if digit1 > 0x2F && digit1 < 0x3A {
			returnvalue = digit1 - 0x30
		}
		// Value is between ASCII 'A'.. 'F'
		if digit1 > 0x40 && digit1 < 0x47 {
			returnvalue = digit1 - 0x37
		}
		returnvalue = returnvalue << 4
	}

	if digit2 > 0x2F && digit2 < 0x3A || digit2 > 0x40 && digit2 < 0x47 {
		// Value is between ASCII '0'..'9' and ASCII 'A'..'F'
		if digit2 > 0x2F && digit2 < 0x3A {
			returnvalue |= digit2 - 0x30
		}
		// Value is between ASCII 'A'.. 'F'
		if digit2 > 0x40 && digit2 < 0x47 {
			returnvalue |= digit2 - 0x37
		}

	}
	return returnvalue
}

// LoadSrec --------------------------------------------------------------------------------------------------------
// Name: LoadSrec
// Function: Load Motorola S-Record file from the disk and parse it, then decode all the data to binary and store
//
//	that in the appropriate buffer
//
// Parameters: Full path to the file that shall be opened, Target Area in MCU, Pointer to variable where length shall be stored
// Returns: Result of operation
// ----------------------------------------------------------------------------------------------------------------
func LoadSrec(path string, targetarea uint8, objectlength *uint16) int {

	var address uint16

	*objectlength = 0
	srec, err := os.Open(path)
	if err != nil {
		fmt.Println(" Error opening file! ")
	}
	defer srec.Close()
	srecords := bufio.NewScanner(srec)
	for srecords.Scan() {
		line := srecords.Text()
		// Each line of the S-record is parsed here
		if line[0] == 'S' && line[1] == '1' {
			// Valid S-Record, extract record length

			len := asciihex2bin(line[2], line[3])
			// The length value includes 3 extra i.e. address and check byte, so we subtract to get total size of bytes
			len -= 3
			address = uint16(asciihex2bin(line[4], line[5]))
			address = address << 8
			address |= uint16(asciihex2bin(line[6], line[7]))

			// We now have the address and we have the data, see where we must store it
			if targetarea == RAM_0050 {
				// Target memory is the MCU RAM, the address supplied must fall in that range
				if address < 0x0050 && address > 0x00BF {
					fmt.Println(" Error: S-Record address falls outside of allowable memory range!")
					return -1
				}
				// The very first S-record is usually where the program starts, so we grab that as the start address of the program
				if RAM_PROGRAM_START == 0 {
					RAM_PROGRAM_START = address
				}
				// Range check is OK, decode s-record to the buffer
				var offset = 8
				for n := 0; n < int(len); n++ {
					RAM[(address-0x50)+uint16(n)] = asciihex2bin(line[offset], line[offset+1])
					offset += 2
					*objectlength++
				}
			}
		}
	}

	return 0
}

// -------------------------------------------------------------------------------------------------------------------
// Name: DumpMemory
// Function: Hex dump to the console, the memory area passed by reference and the size of the passed memory area
// Parameters: Pointer to buffer, Size of memory area (in bytes)
// Returns: void
// -------------------------------------------------------------------------------------------------------------------
func DumpMemory(buffer []byte, size int, offset uint16) {

	var addr uint16 = 0
	for {
		fmt.Printf("%04X:   %02X %02X %02X %02X %02X %02X %02X %02X    %02X %02X %02X %02X %02X %02X %02X %02X |\r\n", addr+offset,
			buffer[addr+0], buffer[addr+1], buffer[addr+2], buffer[addr+3], buffer[addr+4], buffer[addr+5], buffer[addr+6], buffer[addr+7],
			buffer[addr+8], buffer[addr+9], buffer[addr+10], buffer[addr+11], buffer[addr+12], buffer[addr+13], buffer[addr+14], buffer[addr+15])
		addr += 16
		if addr >= uint16(size) {
			break
		}
	}
}

// -------------------------------------------------------------------------------------------------------------------
// Main Function
// -------------------------------------------------------------------------------------------------------------------
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
		fmt.Println("Program will now quit!")
		os.Exit(0)
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
	fmt.Println(" * TEST    - Load test program into HC05 and check response (supports official boards and MIDON PROG05 programmer)")
	fmt.Println(" * DUMP    - Dump buffer by area (A: RAM, B: PROM ($20-$4F)")
	fmt.Println(" * DEMO    - Load simple demonstration program into HC05 that toggles PORT A pins (use this to confirm your MCU is OK)")
	fmt.Println(" * LOADRAM - Load user application into HC05 RAM and execute (specify a .S19 file)")
	fmt.Println(" * LOAD    - Load user application into memory for EPROM programming")
	fmt.Println(" * QUIT    - Quit this program ")

	//--------------------------------------------------------------------------------------
	// User Input Handling
	//--------------------------------------------------------------------------------------
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Printf("\r\n>") // Print initial command prompt
		userinput, _ := reader.ReadString('\n')
		//fmt.Println(userinput) // ONLY RETAINED FOR DEBUGGING, REMOVE IT
		// User input obtained, process it

		// DUMP command
		if strings.Contains(userinput, "DUMP A") {
			fmt.Println("HEX Dump of RAM buffer ($005F - $00FF in the HC05 memory map)")
			DumpMemory(RAM, len(RAM), 0x50)
		}

		// LOADRAM command
		if strings.Contains(userinput, "LOADRAM") {
			fmt.Printf(" Enter path and file name of S-record file: ")
			path, _ := reader.ReadString('\n')
			path = strings.Trim(path, "\n")
			path = strings.Trim(path, "\r")

			// Clear buffer prior to loading
			for n := range RAM {
				RAM[n] = 0
			}

			LoadSrec(path, RAM_0050, &RAM_SIZE_LOADED)
			fmt.Printf("S-Record loaded Successfully. %d bytes written to buffer\r\n", RAM_SIZE_LOADED)
			fmt.Println("Please enable loader (9.4V on IRQ pin) and reset the MCU, then press ENTER when ready!")
			anykey, _ := reader.ReadByte()
			if anykey > 0 {
				length = byte(RAM_SIZE_LOADED)
				length++
				//fmt.Printf("Length Indicator (1st byte) = %d\r\n", length)
				fmt.Printf("Upload to target")
				selector = int(RAM_PROGRAM_START - 0x50)
				var p = make([]byte, 1)

				// Send length to the bootloader
				p[0] = length
				_, err := port.Write(p[0:])
				if err == nil {
					for n := 0; n < int(length-1); n++ {
						p[0] = RAM[selector]
						time.Sleep(5 * time.Millisecond)
						_, err := port.Write(p[0:])

						if err != nil {
							fmt.Println("Error writing byte to target.. ")
						} else {
							fmt.Printf(".")
						}
						selector++
					}
					fmt.Println(" DONE!")
					fmt.Println(" Program Running!")
				}
			}
		}

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
