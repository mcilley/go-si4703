//  Copyright (c) Marty Schoch
//  Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file
//  except in compliance with the License. You may obtain a copy of the License at
//    http://www.apache.org/licenses/LICENSE-2.0
//  Unless required by applicable law or agreed to in writing, software distributed under the
//  License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
//  either express or implied. See the License for the specific language governing permissions
//  and limitations under the License.

package si4703

import (
	"bytes"
	"encoding/binary"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/mschoch/go-rds"

	"machine"

	"tinygo.org/x/drivers"
)

const I2C_ADDR = 0x10

// register names
const (
	DEVICEID uint16 = iota
	CHIPID
	POWERCFG
	CHANNEL
	SYSCONFIG1
	SYSCONFIG2
	UNUSED6
	UNUSED7
	UNUSED8
	UNUSED9
	STATUSRSSI
	READCHAN
	RDSA
	RDSB
	RDSC
	RDSD
)

// powercfg
const SMUTE uint16 = 15
const DMUTE uint16 = 14
const FORCEMONO uint16 = 13
const RDSMODE uint16 = 11
const SKMODE uint16 = 10
const SEEKUP uint16 = 9
const SEEK uint16 = 8

// channel
const TUNE uint16 = 15

// sysconfig1
const RDS uint16 = 12
const DE uint16 = 11
const AGC uint16 = 10
const BLNDADJ uint16 = 7

// sysconfig2
const SPACE1 uint16 = 5
const SPACE0 uint16 = 4

// statusrssi
const RDSR uint16 = 15
const STC uint16 = 14
const SFBL uint16 = 13
const AFCRL uint16 = 12
const RDSS uint16 = 11
const STEREO uint16 = 8

type Device struct {
	bus       drivers.I2C
	addr      uint16
	registers []uint16
	rdsinfo   *rds.RDSInfo
	reset     machine.Pin
}

func New(bus drivers.I2C) Device {
	return Device{
		bus:       bus,
		addr:      I2C_ADDR,
		registers: make([]uint16, 16),
		reset:     machine.Pin(machine.GPIO15),
	}
}

func (d *Device) Configure() (err error) {
	d.rdsinfo = rds.NewRDSInfo()

	// do some manual GPIO to initialize the device
	// err = rpio.Open()
	// if err != nil {
	// 	return err
	// }

	d.reset.Configure(machine.PinConfig{Mode: machine.PinOutput})

	d.reset.Low()
	time.Sleep(1 * time.Second)
	d.reset.High()
	time.Sleep(1 * time.Second)

	// read
	d.readRegisters()
	// enable the oscillator
	d.registers[UNUSED7] = 0x8100
	// update
	d.updateRegisters()

	// wait for clock to settle
	time.Sleep(500 * time.Millisecond)

	// read
	d.readRegisters()
	// enable the IC
	d.registers[POWERCFG] = 0x0001
	d.registers[SYSCONFIG1] = d.registers[SYSCONFIG1] | (1 << RDS)
	d.registers[SYSCONFIG2] = d.registers[SYSCONFIG2] & 0xFFF0 // clear volume
	d.registers[SYSCONFIG2] = d.registers[SYSCONFIG2] | 0x0001 // set to lowest
	// update
	d.updateRegisters()

	// wait max powerup time
	time.Sleep(110 * time.Millisecond)

	return
}

func (d *Device) Close() error {
	println("turning off chip")
	// read
	d.readRegisters()
	// disable the IC
	d.registers[POWERCFG] = 0x0000
	d.updateRegisters()
	return nil
}

func (d *Device) DisableSoftMute() {
	d.readRegisters()
	d.registers[POWERCFG] = d.registers[POWERCFG] | (1 << SMUTE)
	d.updateRegisters()
}

func (d *Device) DisableMute() {
	d.readRegisters()
	d.registers[POWERCFG] = d.registers[POWERCFG] | (1 << DMUTE)
	d.updateRegisters()
}

func (d *Device) EnableMute() {
	d.readRegisters()
	d.registers[POWERCFG] = d.registers[POWERCFG] & 0xBFFF
	d.updateRegisters()
}

func (d *Device) readRegisters() {

	// with i2c we first write an address we want to read
	// however, this device interprets that address
	// as the first byte of the register at 0x2
	// so in order to use the ReadByteBlock method
	// without destroying our data, we have to write the
	// correct value back there

	buf := new(bytes.Buffer)
	binary.Write(buf, binary.BigEndian, d.registers[0x2])
	bufbytes := buf.Bytes()

	data := make([]byte, 32)
	var err error
	if err = d.bus.Tx(d.addr, bufbytes, data); err != nil {
		return
	}

	//log.Printf("read bytes %v", data)

	counter := 0
	for x := 0x0A; ; x++ {
		if x == 0x10 {
			x = 0
		}
		p := bytes.NewBuffer(data[counter : counter+2])
		err = binary.Read(p, binary.BigEndian, &d.registers[x])
		if err != nil {
			log.Printf("error reading: %v", err)
			return
		}
		counter = counter + 2
		if x == 0x09 {
			break
		}
	}

	//log.Printf("self: %v", d)
}

func (d *Device) updateRegisters() {
	p := new(bytes.Buffer)
	for x := 0x02; x < 0x08; x++ {
		binary.Write(p, binary.BigEndian, d.registers[x])
	}

	bytes := p.Bytes()
	log.Printf("output bytes is %v", bytes)

	err := d.bus.Tx(d.addr, bytes, bytes[1:])
	if err != nil {
		log.Printf("error writing: %v")
	}

	//d.readRegisters()
}

func (d *Device) SetVolume(volume uint16) {
	d.readRegisters()
	if volume < 0 {
		volume = 0
	}
	if volume > 15 {
		volume = 15
	}
	d.registers[SYSCONFIG2] = d.registers[SYSCONFIG2] & 0xFFF0
	d.registers[SYSCONFIG2] = d.registers[SYSCONFIG2] | volume
	d.updateRegisters()
}

func (d *Device) SetChannel(channel uint16) {
	newChannel := channel * 10
	newChannel = newChannel - 8750
	newChannel = newChannel / 20

	d.readRegisters()
	d.registers[CHANNEL] = d.registers[CHANNEL] & 0xFE00
	d.registers[CHANNEL] = d.registers[CHANNEL] | newChannel
	d.registers[CHANNEL] = d.registers[CHANNEL] | (1 << TUNE)

	log.Printf("Attempting to tune and fart")
	d.updateRegisters()

	// wait for tuning to complete
	for {
		d.readRegisters()
		if d.registers[STATUSRSSI]&(1<<STC) != 0 {
			//log.Printf("Tuning Complete")
			break
		}
	}

	// clear out old RDS info
	d.rdsinfo = rds.NewRDSInfo()

	// clear the tune bit
	d.registers[CHANNEL] = d.registers[CHANNEL] &^ (1 << TUNE)
	d.updateRegisters()

	// now wait for for STC to be cleared
	for {
		d.readRegisters()
		if d.registers[STATUSRSSI]&(1<<STC) == 0 {
			//log.Printf("STC Cleared")
			break
		}
	}

	log.Printf("Tuned to %s", d.printReadChannel(d.registers[READCHAN]))
}

func (d *Device) Seek(dir byte) {
	d.readRegisters()
	if dir == 1 {
		log.Printf("Seeking UP")
		d.registers[POWERCFG] = d.registers[POWERCFG] | (1 << SEEKUP)
	} else {
		log.Printf("Seeking DOWN")
		d.registers[POWERCFG] = d.registers[POWERCFG] &^ (1 << SEEKUP)
	}
	d.registers[POWERCFG] = d.registers[POWERCFG] | (1 << SEEK)

	// start seek

	d.updateRegisters()

	// wait for seek to complete
	for {
		d.readRegisters()
		if d.registers[STATUSRSSI]&(1<<STC) != 0 {
			//log.Printf("Seek Complete")
			break
		}
	}

	// clear out old RDS info
	d.rdsinfo = rds.NewRDSInfo()

	// clear the seek bit
	d.registers[POWERCFG] = d.registers[POWERCFG] &^ (1 << SEEK)

	// now wait for for STC to be cleared
	for {
		d.readRegisters()
		if d.registers[STATUSRSSI]&(1<<STC) == 0 {
			//log.Printf("STC Cleared")
			break
		}
	}
	log.Printf("Seeked to %s", d.printReadChannel(d.registers[READCHAN]))
}

func (d *Device) String() string {
	rv := "--------------------------------------------------------------------------------\n"
	rv = rv + d.printDeviceID(d.registers[DEVICEID])
	rv = rv + d.printChipID(d.registers[CHIPID])
	rv = rv + d.printPowerCfg(d.registers[POWERCFG])
	rv = rv + d.printChannel(d.registers[CHANNEL])
	rv = rv + d.printSysConfig1(d.registers[SYSCONFIG1])
	rv = rv + d.printStatusRSSI(d.registers[STATUSRSSI])
	rv = rv + d.printReadChannel(d.registers[READCHAN])
	rv = rv + d.printRDS("A", d.registers[RDSA])
	rv = rv + d.printRDS("B", d.registers[RDSB])
	rv = rv + d.printRDS("C", d.registers[RDSC])
	rv = rv + d.printRDS("D", d.registers[RDSD])
	rv = rv + "--------------------------------------------------------------------------------\n\n"
	return rv
}

func (d *Device) printDeviceID(deviceid uint16) string {
	var rv strings.Builder
	rv.WriteString("part Number: ")
	rv.WriteString(d.printPartNumber(byte(deviceid >> 12)))
	rv.WriteString("\n")
	rv.WriteString("Manufacturer: 0x")
	rv.WriteString(strconv.Itoa(int(deviceid & 0xFFF)))
	rv.WriteString("\n")
	return rv.String()
}

func (d *Device) printPartNumber(num byte) string {
	switch num {
	case 0x01:
		return "Si4702/03"
	default:
		return "Unknown"
	}
}

func (d *Device) printChipID(chipid uint16) string {
	var rv strings.Builder
	rv.WriteString("Chip Version: ")
	rv.WriteString(d.printChipVersion(byte(chipid >> 10)))
	rv.WriteString("\n")
	rv.WriteString("Device: ")
	rv.WriteString(d.printDevice(byte((chipid & 0x1FF) >> 6)))
	rv.WriteString("\n")
	rv.WriteString("Firmware Version: ")
	rv.WriteString(d.printFirmwareVersion(byte(chipid & 0x1F)))
	rv.WriteString("\n")

	return rv.String()
}

func (d *Device) printChipVersion(rev byte) string {
	switch rev {
	case 0x04:
		return "Rev C"
	default:
		return "Unknown"
	}
}

func (d *Device) printDevice(dev byte) string {
	switch dev {
	case 0x0:
		return "Si4702 (off)"
	case 0x1:
		return "Si4702 (on)"
	case 0x8:
		return "Si4703 (off)"
	case 0x9:
		return "Si4703 (on)"
	default:
		return "Unknown"
	}
}

func (d *Device) printFirmwareVersion(rev byte) string {
	var rv strings.Builder
	switch rev {
	case 0x0:
		return "Off"
	default:
		rv.WriteByte(rev)
		return rv.String()
	}
}

func (d *Device) printPowerCfg(powercfg uint16) string {
	var rv strings.Builder
	rv.WriteString("Soft Mute: ")
	rv.WriteString(d.printMute(byte(powercfg >> SMUTE)))
	rv.WriteString("\n")
	rv.WriteString("Mute: ")
	rv.WriteString(d.printMute(byte(powercfg >> DMUTE & 0x1)))
	rv.WriteString("\n")
	rv.WriteString("Force Mono: ")
	rv.WriteString(d.printEnabled(byte(powercfg >> FORCEMONO & 0x1)))
	rv.WriteString("\n")
	rv.WriteString("RDS Mode: ")
	rv.WriteString(d.printRDSMode(byte(powercfg >> RDSMODE & 0x1)))
	rv.WriteString("\n")
	rv.WriteString("Seek Mode: ")
	rv.WriteString(d.printSeekMode(byte(powercfg >> SKMODE & 0x1)))
	rv.WriteString("\n")
	rv.WriteString("Seek Direction: ")
	rv.WriteString(d.printSeekDirection(byte(powercfg >> SEEKUP & 0x1)))
	rv.WriteString("\n")
	rv.WriteString("Seek: ")
	rv.WriteString(d.printEnabled(byte(powercfg >> SEEK & 0x1)))
	rv.WriteString("\n")
	rv.WriteString("Power-Up Disable: ")
	rv.WriteString(d.printPower(byte(powercfg&0x3f) >> 6))
	rv.WriteString("\n")
	rv.WriteString("Power-Up Enable: ")
	rv.WriteString(d.printPower(byte(powercfg & 0x1)))
	rv.WriteString("\n")

	return rv.String()
}

func (d *Device) printMute(mute byte) string {
	switch mute {
	case 0x0:
		return "Enabled"
	default:
		return "Disabled"
	}
}

func (d *Device) printStereoMonoActual(mono byte) string {
	switch mono {
	case 0x0:
		return "Mono"
	default:
		return "Stereo"
	}
}

func (d *Device) printRDSMode(rds byte) string {
	switch rds {
	case 0x0:
		return "Standard"
	default:
		return "Verbose"
	}
}

func (d *Device) printSeekMode(seek byte) string {
	switch seek {
	case 0x0:
		return "Wrap"
	default:
		return "Stop"
	}
}

func (d *Device) printSeekDirection(seek byte) string {
	switch seek {
	case 0x0:
		return "Down"
	default:
		return "Up"
	}
}

func (d *Device) printEnabled(seek byte) string {
	switch seek {
	case 0x0:
		return "Disabled"
	default:
		return "Enabled"
	}
}

func (d *Device) printPower(power byte) string {
	switch power {
	case 0x0:
		return "Default"
	default:
		return "On"
	}
}

func (d *Device) printChannel(tune uint16) string {
	var rv strings.Builder
	rv.WriteString("Tune: ")
	rv.WriteString(d.printEnabled(byte(tune >> TUNE)))
	rv.WriteString("\n")
	rv.WriteString("Tune Channel: ")
	rv.WriteString(d.printChannelNumber(tune & 0x1FF))
	rv.WriteString("\n")

	return rv.String()
}

func (d *Device) printChannelNumber(channel uint16) string {
	var rv strings.Builder
	band := 0      // FIXME use actual band
	spacing := 200 // FIXME use actual spacing
	switch band {
	case 0:
		freq := ((float64(channel) * 20) + 8750) / 100
		rv.WriteString(strconv.FormatFloat(freq, 'f', 2, 64))
		rv.WriteString("MHz")
		return rv.String()
	case 1:
		freq := (float64(spacing) * float64(channel)) + 76.0
		rv.WriteString(strconv.FormatFloat(freq, 'f', 2, 64))
		rv.WriteString("MHz")
	default:
		return "Unknown"
	}
}

func (d *Device) printDeemphasis(de byte) string {
	switch de {
	case 0:
		return "75μs"
	case 1:
		return "50μs"
	default:
		return "Unknown"
	}
}

func (d *Device) printSMBlend(blndadj byte) string {
	switch blndadj {
	case 0:
		return "31–49 RSSI dBµV (default)"
	case 1:
		return "37–55 RSSI dBµV (+6 dB)"
	case 2:
		return "19–37 RSSI dBµV (–12 dB)"
	case 3:
		return "25–43 RSSI dBµV (–6 dB)"
	default:
		return "Unknown"
	}
}

func (d *Device) printSysConfig1(sysconf uint16) string {
	var rv strings.Builder
	rv.WriteString("RDS Interrupt: ")
	rv.WriteString(d.printEnabled(byte(sysconf >> RDSR)))
	rv.WriteString("\n")
	rv.WriteString("Seek/Tune Complete Interrupt: ")
	rv.WriteString(d.printEnabled(byte(sysconf >> STC & 0x1)))
	rv.WriteString("\n")
	rv.WriteString("RDS: ")
	rv.WriteString(d.printEnabled(byte(sysconf >> RDS & 0x1)))
	rv.WriteString("\n")
	rv.WriteString("De-emphasis: ")
	rv.WriteString(d.printDeemphasis(byte(sysconf >> DE & 0x1)))
	rv.WriteString("\n")
	rv.WriteString("AGC: ")
	rv.WriteString(d.printEnabled(byte(sysconf >> AGC & 0x1)))
	rv.WriteString("\n")
	rv.WriteString("Stereo/Mono Blend Adjustment: ")
	rv.WriteString(d.printSMBlend(byte(sysconf >> BLNDADJ & 0x3)))
	rv.WriteString("\n")

	return rv.String()
}

func (d *Device) printRDSReady(rdsr byte) string {
	switch rdsr {
	case 0x0:
		return "No RDS group ready"
	default:
		return "New RDS group ready"
	}
}

func (d *Device) printComplete(com byte) string {
	switch com {
	case 0x0:
		return "Not complete"
	default:
		return "Complete"
	}
}

func (d *Device) printSeekFailBandLimit(sfbl byte) string {
	switch sfbl {
	case 0x0:
		return "Seek successful"
	default:
		return "Seek failure/Band limit reached"
	}
}

func (d *Device) printAFCRail(afcrl byte) string {
	switch afcrl {
	case 0x0:
		return "AFC not railed"
	default:
		return "AFC railed"
	}
}

func (d *Device) printSynchronized(rdss byte) string {
	switch rdss {
	case 0x0:
		return "RDS decoder not synchronized"
	default:
		return "RDS decoder synchronized"
	}
}

func (d *Device) printStatusRSSI(status uint16) string {
	var rv strings.Builder

	rv.WriteString("RDS Ready: ")
	rv.WriteString(d.printRDSReady(byte(status >> RDSR)))
	rv.WriteString("\n")
	rv.WriteString("Seek/Tune Complete: ")
	rv.WriteString(d.printComplete(byte(status >> STC & 0x1)))
	rv.WriteString("\n")
	rv.WriteString("Seek Fail/Band Limit: ")
	rv.WriteString(d.printSeekFailBandLimit(byte(status >> SFBL & 0x1)))
	rv.WriteString("\n")
	rv.WriteString("AFC Rail: ")
	rv.WriteString(d.printAFCRail(byte(status >> AFCRL & 0x1)))
	rv.WriteString("\n")
	rv.WriteString("RDS Synchronized: ")
	rv.WriteString(d.printSynchronized(byte(status >> RDSS & 0x1)))
	rv.WriteString("\n")
	rv.WriteString("Stereo/Mono: ")
	rv.WriteString(d.printStereoMonoActual(byte(status >> STEREO & 0x1)))
	rv.WriteString("\n")
	rv.WriteString("RSSI: ")
	rv.WriteString(strconv.Itoa(int(status & 0x7F)))
	rv.WriteString("dBµV")
	rv.WriteString("\n")

	return rv.String()
}

func (d *Device) printReadChannel(readChannel uint16) string {
	var rv strings.Builder
	rv.WriteString("Channel: ")
	rv.WriteString(d.printChannelNumber(readChannel & 0x1FF))
	rv.WriteString("\n")
	return rv.String()
}

func (d *Device) PollRDS() {
	for {
		select {
		case <-time.After(40 * time.Millisecond):
			d.readRegisters()
			if byte(d.registers[STATUSRSSI]>>RDSR) == 1 {
				// d.rdsinfo.PI = d.registers[RDSA]
				// d.rdsinfo.ProgramType = d.registers[RDSB] >> 5 & 0x1F
				// rv := "RDS Ready\n"
				// rv = rv + d.printRDS("A", d.registers[RDSA])
				// rv = rv + d.printRDS("B", d.registers[RDSB])
				// rv = rv + d.printRDS("C", d.registers[RDSC])
				// rv = rv + d.printRDS("D", d.registers[RDSD])
				// rv = rv + fmt.Sprintf("PI code: %d %d\n", d.registers[RDSA]>>8, d.registers[RDSA]&0xFF)
				// rv = rv + fmt.Sprintf("Group type: %d\n", d.registers[RDSB]>>12)
				// rv = rv + fmt.Sprintf("Version: %d\n", d.registers[RDSB]>>11&0x1)
				// rv = rv + fmt.Sprintf("Traffic Program Code: %d\n", d.registers[RDSB]>>10&0x1)
				// rv = rv + fmt.Sprintf("Program Type: %d\n", d.registers[RDSB]>>5&0x1F)
				//fmt.Printf("%s", rv)
				d.rdsinfo.Update(d.registers[RDSA], d.registers[RDSB], d.registers[RDSC], d.registers[RDSD])
				println("%v\n", d.rdsinfo)
			}
		}
	}
}

func (d *Device) printRDS(prefix string, rds uint16) string {
	var rv strings.Builder
	rv.WriteString(prefix)
	rv.WriteString(": ")
	rv.WriteString(string(rds >> 8))
	rv.WriteString(string(rds & 0xFF))
	rv.WriteString("\n")
	return rv.String()
}
