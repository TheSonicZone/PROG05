package main

import (
	"bufio"
	"encoding/hex"
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

// Menu Selection constants
const DUMP_BUFFER_REGION_A = 10

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

var userinput string
var errtype error
var addr_hi uint8
var addr_lo uint8
var data_byte uint8
var mcuaddress uint16
var mcudump = make([]byte, 8192)

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
		return -1
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
// Name: ReadByteFromMCU
// Function: Reads byte from specified address in the HC05
// Parameters: Address, pointer to location where read data will be stored
// Returns: 0 if OK, -1 if error
// -------------------------------------------------------------------------------------------------------------------
func ReadByteFromMCU(address uint16, data *uint8) int {

	var readtimeout = 0
	addr_hi = uint8((address >> 8) & 0xFF)
	addr_lo = uint8(address & 0xFF)
	//fmt.Printf(" [DEBUG] Address bytes: %02X %02X\r\n", addr_hi, addr_lo)

	// Clear serial receive buffer
	for i := 0; i < 1024; i++ {
		rxbuffer[i] = 0
	}
	rxbuffercount = 0

	// Then, transmit address to program running in the HC05
	var p = make([]byte, 1)
	p[0] = addr_hi
	_, err := port.Write(p[0:])
	if err != nil {
		fmt.Println("Error Sending byte on serial port...(1) ")
		return -1
	}
	time.Sleep(1 * time.Millisecond)
	p[0] = addr_lo
	_, err = port.Write(p[0:])
	if err != nil {
		fmt.Println("Error Sending byte on serial port...(2) ")
		return -1
	}
	readtimeout = 500
	for {
		time.Sleep(10 * time.Microsecond) // Sleep for 10uS
		if rxbuffercount > 0 {
			break
		}
		readtimeout--
		if readtimeout == 0 {
			break
		}
	}

	// Get byte received
	if readtimeout == 0 {
		fmt.Println(" Response timeout...")
		return -1
	}
	if rxbuffercount > 0 {
		readbyte := rxbuffer[0]
		//fmt.Printf(" Value Read: %02X\r\n", readbyte)
		*data = readbyte
		return 0
	} else {
		fmt.Println(" Error reading memory")
		return -1
	}

}

// ------------------------------------------------------------------------------
// Name: PrintHC05LoaderInstruction
// Function: Print out instructions to invoke the HC05 bootloader to the console
// ------------------------------------------------------------------------------
func PrintHC05LoaderInstruction() {
	fmt.Println("Please enable loader either by: ")
	fmt.Println("  * MC68HC05PGMR: S3-S5 = OFF, S6 = ON, shunt across Pin 1 & 2 of J1")
	fmt.Println("  * MIDON PROG05: shunt across pins 1 & 2 of J1")
	fmt.Println("Then, release reset by:")
	fmt.Println("  * MC68HC05PGMR: switch S2 from RESET -> OUT")
	fmt.Println("  * MIDON PROG05: Press and release SW1")
	fmt.Println("  **** PRESS ENTER WHEN READY ***")
}

// Name: ShowCommands
// Function: Print out all the available commands to the console
// ----------------------------------------------------------------
func ShowCommands() {
	fmt.Println("***************** PROG05 COMMAND OPTIONS *********************")
	fmt.Println("Available Commands:")
	fmt.Println(" * TEST    - Load test program into HC05 and check response (supports official boards and MIDON PROG05 programmer)")
	fmt.Println(" * DUMP    - Dump internal buffer by area (A: RAM ($20-$4F), B: PROM ($160-$1EFF))")
	fmt.Println(" * DEMO    - Load simple demonstration program into HC05 that toggles PORT A pins (use this to confirm your MCU is OK)")
	fmt.Println(" * LOADRAM - Load user application into HC05 RAM and execute (specify a .S19 file)")
	fmt.Println(" * LOAD    - Load user application into memory for EPROM programming")
	fmt.Println(" * READ    - Read a specified memory address in the HC05 memory map")
	fmt.Println(" * WRITE   - Write a specified memory address in the HC05 memory map")
	fmt.Println(" * DUMPMCU - Read entire HC05 address space and display as hexdump (only works if device is unsecured)")
	fmt.Println(" * QUIT    - Quit this program ")
}

// -------------------------------------------------------------------------------------------------------------------
// Main Function
// -------------------------------------------------------------------------------------------------------------------
func main() {
	fmt.Println("                                              ")
	fmt.Println("╔════════════════════════════════════════════╗")
	fmt.Println("║   PROG05 - A modern 68HC705C8 Programmer   ║")
	fmt.Println("║        Version 1.00 - by Sonic2k           ║")
	fmt.Println("╚════════════════════════════════════════════╝")
	fmt.Println("                                              ")

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
	fmt.Println("   ** READY TO ACCESS TARGET MC68HC705C8  **   ")
	go SerialRx()
	ShowCommands()

	//--------------------------------------------------------------------------------------
	// User Input Handling
	//--------------------------------------------------------------------------------------
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf(">") // Print initial command prompt
	for {
	CmdInput:

		userinput, errtype = reader.ReadString('\n')

		switch userinput {

		case "?\r\n":
			ShowCommands()
			fmt.Printf(">")
			break
		case "\r\n":
			fmt.Printf(">")
			break

		case "DUMPMCU\r\n":
			//------------------------------------------------------------------
			// Dump entire MCU address space 0x0000 - 0x1FFFF
			//------------------------------------------------------------------
			// First we load an applet to the HC05 to access the memory map
			pwd, _ := os.Getwd()
			res := LoadSrec(pwd+"/srec/memread.s19", RAM_0050, &RAM_SIZE_LOADED)
			if res != 0 {
				goto CmdInput
			}
			fmt.Println("Preparing to dump HC05...")
			PrintHC05LoaderInstruction()
			anykey, _ := reader.ReadByte()
			if anykey > 0 {
				length = byte(RAM_SIZE_LOADED)
				length++
				//fmt.Printf("Length Indicator (1st byte) = %d\r\n", length)
				fmt.Printf("Initialising target")
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
				}
			}
			// Applet is in the HC05, now we can interact with it
			reader.Discard(1)
			for i := 0; i < 8192; i++ {
				mcudump[i] = 0xFF
			}

			var OPTIONREG uint8 = 0
			var MASK_OPT_REG1 uint8 = 0
			var MASK_OPT_REG2 uint8 = 0
			ReadByteFromMCU(0x1FDF, &OPTIONREG)
			ReadByteFromMCU(0x1FF0, &MASK_OPT_REG1)
			ReadByteFromMCU(0x1FF1, &MASK_OPT_REG2)
			fmt.Printf(" OPTION Register = %02X\r\n", OPTIONREG)
			fmt.Printf(" MASK OPTION Register 1 = %02X\r\n", MASK_OPT_REG1)
			fmt.Printf(" MASK OPTION Register 2 = %02X\r\n", MASK_OPT_REG2)

			// Loop to dump entire memory range
			var mcuaddress uint16 = 0
			for {
				ReadByteFromMCU(mcuaddress, &mcudump[mcuaddress])
				fmt.Printf(" Address: %04X \r", mcuaddress)
				mcuaddress++
				if mcuaddress > 8191 {
					break
				}
			}
			fmt.Println(" Entire HC05 memory space read successfully")
			DumpMemory(mcudump, 8192, 0)
			fmt.Printf(">")
			break

		case "WRITE\r\n":
			//------------------------------------------------------------------
			// WRITE command
			//-----------------------------------------------------------------
			if strings.Contains(userinput, "WRITE") {
				// First we load an applet to the HC05 to access the memory map
				pwd, _ := os.Getwd()
				res := LoadSrec(pwd+"/srec/memwrite.s19", RAM_0050, &RAM_SIZE_LOADED)
				if res != 0 {
					goto CmdInput
				}
				fmt.Println("Preparing to access HC05...")
				PrintHC05LoaderInstruction()
				anykey, _ := reader.ReadByte()
				if anykey > 0 {
					length = byte(RAM_SIZE_LOADED)
					length++
					//fmt.Printf("Length Indicator (1st byte) = %d\r\n", length)
					fmt.Printf("Initialising target")
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
					}
				}
				// Applet is in the HC05, now we can interact with it
				fmt.Println("     -- HC05 is in access mode, enter Q to exit and return --    ")
				reader.Discard(1)
				for {
				Reloop2:
					fmt.Printf("Enter address to be written (in hexadecimal):")
					keyinput, _ := reader.ReadString('\n')

					if keyinput == "\r\n" {
						goto Reloop2
					}

					if strings.Contains(keyinput, "Q\r\n") {
						fmt.Println("     -- HC05 access mode terminated --    ")
						break
					} else {

						address, err := hex.DecodeString(keyinput[:4])
						if err != nil {
							fmt.Println(" Invalid user input- must be 4 hexadecimal digits (format: nnnn)")
						} else {
							// User entered an address in hexadecimal, now the user is asked for the value in hexadecimal
						Reloop3:
							fmt.Printf("Enter data to be written (in hexadecimal):")
							keyinput_b, _ := reader.ReadString('\n')
							if keyinput_b == "\r\n" {
								goto Reloop3
							}
							if strings.Contains(keyinput, "Q\r\n") {
								fmt.Println("     -- HC05 access mode terminated --    ")
								break
							} else {
								hexdata, err := hex.DecodeString(keyinput_b[:2])
								if err != nil {
									fmt.Println(" Invalid user input- must be 2 hexadecimal digits (format: nn)")
									goto Reloop3
								}

								// Transmit address bytes (16 bits)
								addr_hi = address[0]
								addr_lo = address[1]
								data_byte = hexdata[0]
								fmt.Printf(" [DEBUG] Address bytes + Data : %02X %02X   %02X\r\n", addr_hi, addr_lo, data_byte)

								// Clear receive buffer

								for i := 0; i < 1024; i++ {
									rxbuffer[i] = 0
								}
								rxbuffercount = 0

								//	Then, transmit address..
								var p = make([]byte, 1)
								p[0] = addr_hi
								_, err = port.Write(p[0:])
								if err != nil {
									fmt.Println("Error Sending byte on serial port...(1) ")
								}
								time.Sleep(1 * time.Millisecond)
								p[0] = addr_lo
								_, err = port.Write(p[0:])
								if err != nil {
									fmt.Println("Error Sending byte on serial port...(2) ")
								}
								time.Sleep(1 * time.Millisecond)
								p[0] = data_byte
								_, err = port.Write(p[0:])
								if err != nil {
									fmt.Println("Error Sending byte on serial port...(3) ")
								}
								fmt.Println("Write operation complete...")
							}
						}
					}
				}
			}
			fmt.Printf(">")
			break

		case "READ\r\n":
			//------------------------------------------------------------------
			// READ command
			//-----------------------------------------------------------------
			if strings.Contains(userinput, "READ") {
				// First we load an applet to the HC05 to access the memory map
				pwd, _ := os.Getwd()
				res := LoadSrec(pwd+"/srec/memread.s19", RAM_0050, &RAM_SIZE_LOADED)
				if res != 0 {
					goto CmdInput
				}
				fmt.Println("Preparing to access HC05...")
				PrintHC05LoaderInstruction()
				anykey, _ := reader.ReadByte()
				if anykey > 0 {
					length = byte(RAM_SIZE_LOADED)
					length++
					//fmt.Printf("Length Indicator (1st byte) = %d\r\n", length)
					fmt.Printf("Initialising target")
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
					}
				}
				// Applet is in the HC05, now we can interact with it
				fmt.Println("     -- HC05 is in access mode, enter Q to exit and return --    ")
				reader.Discard(1)
				for {
				Reloop:
					fmt.Printf("Enter address to be read (in hexadecimal):")
					keyinput, _ := reader.ReadString('\n')

					if keyinput == "\r\n" {
						goto Reloop
					}

					if strings.Contains(keyinput, "Q\r\n") {
						fmt.Println("     -- HC05 access mode terminated --    ")
						break
					} else {
						// We assume the value entered is valid, so we try and convert it to an integer

						address, err := hex.DecodeString(keyinput[:4])
						if err != nil {
							fmt.Println(" Invalid user input- must be 4 hexadecimal digits (format: nnnn)")
						} else {
							// Transmit address bytes (16 bits)
							addr_hi = address[0]
							addr_lo = address[1]
							fmt.Printf(" [DEBUG] Address bytes: %02X %02X\r\n", addr_hi, addr_lo)

							// Clear receive buffer

							for i := 0; i < 1024; i++ {
								rxbuffer[i] = 0
							}
							rxbuffercount = 0

							//Then, transmit address..
							var p = make([]byte, 1)
							p[0] = addr_hi
							_, err := port.Write(p[0:])
							if err != nil {
								fmt.Println("Error Sending byte on serial port...(1) ")
							}
							time.Sleep(1 * time.Millisecond)
							p[0] = addr_lo
							_, err = port.Write(p[0:])
							if err != nil {
								fmt.Println("Error Sending byte on serial port...(2) ")
							}
							time.Sleep(100 * time.Millisecond)

							// Get byte received
							if rxbuffercount > 0 {
								readbyte := rxbuffer[0]
								fmt.Printf(" Value Read: %02X\r\n", readbyte)
							} else {
								fmt.Println(" Error reading memory")
							}

						}
					}
				}
			}
			fmt.Printf(">")
			break

		case "DUMP A\r\n":
			//------------------------------------------------------------------
			// DUMP command
			//-----------------------------------------------------------------
			if strings.Contains(userinput, "DUMP A") {
				fmt.Println("HEX Dump of RAM buffer ($005F - $00FF in the HC05 memory map)")
				DumpMemory(RAM, len(RAM), 0x50)
			}
			fmt.Printf(">") // Print initial command prompt
			break

		case "LOADRAM\r\n":
			//------------------------------------------------------------------
			// LOADRAM command
			//------------------------------------------------------------------
			if strings.Contains(userinput, "LOADRAM") {
				fmt.Printf(" Enter path and file name of S-record file: ")
				path, _ := reader.ReadString('\n')
				path = strings.Trim(path, "\n")
				path = strings.Trim(path, "\r")

				// Clear buffer prior to loading
				for n := range RAM {
					RAM[n] = 0
				}
				res := LoadSrec(path, RAM_0050, &RAM_SIZE_LOADED)
				if res != 0 {
					goto CmdInput
				}
				fmt.Printf("S-Record loaded Successfully. %d bytes written to buffer\r\n", RAM_SIZE_LOADED)
				PrintHC05LoaderInstruction()
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
			fmt.Printf(">") // Print initial command prompt
			break

		case "QUIT\r\n":
			//--------------
			// Quit command
			//--------------
			if strings.Contains(userinput, "QUIT") {
				port.Close()
				fmt.Println("Program shutdown")
				os.Exit(0)
			}

		case "DEMO\r\n":
			//-------------------------------------------------------------------
			// DEMO command - Load small app into HC05 to allow user to play with it
			// Here we use a small applet loaded in from an s-record file
			//-------------------------------------------------------------------
			if strings.Contains(userinput, "DEMO") {
				for i := 0; i < 1024; i++ {
					rxbuffer[i] = 0
				}
				rxbuffercount = 0
				fmt.Println("Loading DEMO program compatible with MC68HC05PGMR and MIDON PROG05")
				pwd, _ := os.Getwd()
				res := LoadSrec(pwd+"/srec/hc05demo.s19", RAM_0050, &RAM_SIZE_LOADED)
				if res != 0 {
					goto CmdInput
				}
				fmt.Printf("S-Record loaded Successfully. %d bytes written to buffer\r\n", RAM_SIZE_LOADED)
				PrintHC05LoaderInstruction()
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
						fmt.Printf("Demo program should be running - Check PORT A pins for toggling\r\n")

						// Clear buffer and pointer
						for i := 0; i < 1024; i++ {
							rxbuffer[i] = 0
						}
						rxbuffercount = 0

					}
				}
			}
			reader.Discard(1)
			fmt.Printf(">") // Print initial command prompt
			break

		default:
			fmt.Println(" Unknown Command")
			fmt.Printf(">") // Print initial command prompt
			break

		case "TEST\r\n":
			//-------------------------------------------------------------------
			// TEST command - Load small app into HC05 and process its response
			// Here we use a small applet loaded in from an s-record file
			//-------------------------------------------------------------------
			if strings.Contains(userinput, "TEST") {
				for i := 0; i < 1024; i++ {
					rxbuffer[i] = 0
				}
				rxbuffercount = 0
				fmt.Println("Loading test program compatible with MC68HC05PGMR and MIDON PROG05")
				pwd, _ := os.Getwd()
				res := LoadSrec(pwd+"/srec/hc05_gotest.s19", RAM_0050, &RAM_SIZE_LOADED)
				if res != 0 {
					goto CmdInput
				}
				fmt.Printf("S-Record loaded Successfully. %d bytes written to buffer\r\n", RAM_SIZE_LOADED)
				PrintHC05LoaderInstruction()
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
						fmt.Printf("Checking target.... ")
						// Clear buffer and pointer
						for i := 0; i < 1024; i++ {
							rxbuffer[i] = 0
						}
						rxbuffercount = 0

						// Allow time for the HC05 to have sent its string to the host
						time.Sleep(800 * time.Millisecond)
						str1 := string(rxbuffer[:rxbuffercount])
						if strings.Contains(str1, "HC05") {
							fmt.Printf(" [OK]\r\n")
							fmt.Println("Target (68HC705C8) access is Successful")

						} else {
							fmt.Printf(" [FAILED]\r\n")
							fmt.Println("  Check your hardware, clock speed, and confirm HC05 did go into bootloader mode")
						}
						// Clear buffer and pointer
						for i := 0; i < 1024; i++ {
							rxbuffer[i] = 0
						}
						rxbuffercount = 0

					}
				}
			}
			reader.Discard(1)
			fmt.Printf(">") // Print initial command prompt
			break
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
