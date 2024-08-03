***************************************************************
* MEMWRITE.ASM
* Applet for PROG05 to write any address within the HC05 memory map
* Author: Sonic2k
* Date: 9 May 2023
*
* Compatibility: Should work with mask all mask revisions as far
* back as 0C16W. Tested and developed on mask revision 0K08B
***************************************************************

* Definitions of addresses and constants


EPGM       EQU 0              ;PROG BIT0; - Vpp CONTROL BIT
ERASED     EQU $00            ;VALUE OF AN ERASED EPROM BYTE
INSTAT     EQU %01100000      ;INITIAL PORT C LED STATUS
LAT        EQU 2              ;PROG BIT2; - EPROM ADDRESS LATCH BIT
LATCH      EQU %00000100      ;PROG BIT2
MUL        EQU $42            ;OP-CODE FOR MULTIPLY INSTRUCTION
OCF        EQU 6              ;TIMSR        BIT6; - OUTPUT COMPARE FLAG
OLVL       EQU 0              ;TIMCR        BIT0; - TIMER COMPARE OUTPUT LEVEL
RDRF       EQU 5              ;SCSR         BIT5; - RCV DATA REG FULL FLAG
TDRE       EQU 7              ;SCSR         BIT7; - XMIT DATA REG EMPTY FLAG
TEST       EQU 2              ;PORTD        BIT2; - '0' GO BOOT,'1'GO $51 (RAM)
OPTION     EQU $1FDF          ;OPTION REGISTER
TSTREG     EQU $1F            ;TEST REGISTER


*
* I/O DEFINITIONS
*
PORTA   EQU $00    ;PORT A DATA
PORTB   EQU $01    ;PORT B DATA
PORTC   EQU $02    ;PORT C DATA
PORTD   EQU $03    ;PORT D DATA (Input Only!)
DDRA    EQU $04    ;PORT A DDR
DDRB    EQU $05    ;PORT B DDR
DDRC    EQU $06    ;PORT C DDR

*
* SERIAL COMMUNICATIONS INTERFACE REGISTERS
*
BAUD  EQU $0D           ; BAUD RATE CONTROL
SCCR1 EQU $0E           ; SERIAL COMM'S CONTROL REGISTER 1
SCCR2 EQU $0F           ; SERIAL COMM'S CONTROL REGISTER 2
SCSR  EQU $10           ; SERIAL COMM'S STATUS
SCDAT EQU $11           ; SERIAL COMM'S DATA

*
* OTHERS
*


*************************************************************************
* Allocation of variables in RAM
*************************************************************************



* Variables located at address 0xBA to 0xBF
* The first portion is an overlay to allow us to modify the LDA opr,X instruction
**********************************************************************************
    org $BA
opcode    ds      1     ; STA hhll,X
addrhi    ds      1     ; hh
addrlo    ds      1     ; ll - high and low address in memory map
return    ds      1     ; RTS
DataByte  ds      1


********************************************************************************************************
* Locate program in RAM
* RAM1:RAM0 = 0x00 hence 48 PROM bytes at 0x0020 -- 0x005F
*             and 96 bytes of PROM at 0x100, henceforth we
*             allocate our executable code to start at 0x0050 which is the address
*             where the CPU will be directed to start execution from once the loader
*             has written all the received bytes to RAM. Note that execution begins from address 0x0051
*********************************************************************************************************
    org $51

****************
* Program start
****************
start:
        ; Here we set up the SCI to transmit
        ; at standard 9600bps

        LDX #DDRA    ; X <- 4
        CLR SCCR1
        LDA #%00001100
        STA SCCR2
        LDA #$30     ; Baud rate = 9600 bps
        STA BAUD

        ; Initialise the overlay
        LDA #$D7           ; <- STA,X ee ff
        STA opcode
        LDA #$81           ; <- RTS
        STA return

*****
* Here we write test values to the registers to ensure
* we read correctly with 16 bit addresses
*****
        LDA     #$55
        STA     DDRA
        LDA     #$AA
        STA     DDRB            ; So PTAD, PTBD, respectively will = 0x55, 0xAA

****************************************************
* Main processing loop
****************************************************
Loop:
     ; Wait for address high and low bytes
        JSR     Receive
        STA     addrhi
        JSR     Receive
        STA     addrlo
     ; Wait for data byte that will be written to the provided address
        JSR     Receive
        STA     DataByte

     ; Write location to address specified
        LDA     #$00
        TAX
        LDA     DataByte        
        JSR     $BA

        BRA     Loop

****************************************************
* Name: Transmit
* Function: Send byte in A out on SCI
****************************************************
Transmit:
        BRCLR   TDRE,SCSR,Transmit    ; Wait for transmitter to be empty
        STA     SCDAT
        RTS

****************************************************
* Name: Receive
* Function: Poll SCI for received data and store in A
****************************************************
Receive:
        BRCLR   RDRF,SCSR,Receive
        LDA     SCDAT
        RTS

****************************************************
* Name: Delay
* Function: Short delay
****************************************************
Delay:
        LDA #$FF        ; desired delay in A, 0xFF gives around 100mS
oloop:  LDX #$A6

iloop:
        DECX
        BNE iloop
        DECA
        BNE oloop
        RTS
