package log

import (
	"os"
	"strings"
	"testing"

	"errors"
)

func init() {
	Testing = 1
	SetUseColors(false)
}

func TestTime(t *testing.T) {
	Testing = 2
	SetDebugVisible(1)
	defer func() { Testing = 1 }()
	Lvl1("No time")
	if !strings.Contains(TestStr, "1 : (") {
		t.Fatal("Didn't get correct string: ", TestStr)
	}
	SetShowTime(true)
	defer func() { SetShowTime(false) }()
	Lvl1("With time")
	if strings.Contains(TestStr, "1 : (") {
		t.Fatal("Didn't get correct string: ", TestStr)
	}
	if strings.Contains(TestStr, " +") {
		t.Fatal("Didn't get correct string: ", TestStr)
	}
	if !strings.Contains(TestStr, "With time") {
		t.Fatal("Didn't get correct string: ", TestStr)
	}
}

func TestFlags(t *testing.T) {
	test := Testing
	Testing = 2
	lvl := DebugVisible()
	time := ShowTime()
	color := UseColors()
	SetDebugVisible(1)

	os.Setenv("DEBUG_LVL", "")
	os.Setenv("DEBUG_TIME", "")
	os.Setenv("DEBUG_COLOR", "")
	ParseEnv()
	if DebugVisible() != 1 {
		t.Fatal("Debugvisible should be 1")
	}
	if ShowTime() {
		t.Fatal("ShowTime should be false")
	}
	if UseColors() {
		t.Fatal("UseColors should be true")
	}

	os.Setenv("DEBUG_LVL", "3")
	os.Setenv("DEBUG_TIME", "true")
	os.Setenv("DEBUG_COLOR", "false")
	ParseEnv()
	if DebugVisible() != 3 {
		t.Fatal("DebugVisible should be 3")
	}
	if !ShowTime() {
		t.Fatal("ShowTime should be true")
	}
	if UseColors() {
		t.Fatal("UseColors should be false")
	}

	os.Setenv("DEBUG_LVL", "")
	os.Setenv("DEBUG_TIME", "")
	os.Setenv("DEBUG_COLOR", "")
	SetDebugVisible(lvl)
	SetShowTime(time)
	SetUseColors(color)
	Testing = test
}

func TestOutputFuncs(t *testing.T) {
	ErrFatal(checkOutput(func() {
		Lvl1("Testing stdout")
	}, true, false))
	ErrFatal(checkOutput(func() {
		LLvl1("Testing stdout")
	}, true, false))
	ErrFatal(checkOutput(func() {
		Print("Testing stdout")
	}, true, false))
	ErrFatal(checkOutput(func() {
		Warn("Testing stdout")
	}, false, true))
	ErrFatal(checkOutput(func() {
		Error("Testing errout")
	}, false, true))
}

func checkOutput(f func(), wantsStd, wantsErr bool) error {
	f()
	stdStr := getStdOut()
	errStr := getStdErr()
	if wantsStd {
		if len(stdStr) == 0 {
			return errors.New("Stdout was empty")
		}
	} else {
		if len(stdStr) > 0 {
			return errors.New("Stdout was full")
		}
	}
	if wantsErr {
		if len(errStr) == 0 {
			return errors.New("Stderr was empty")
		}
	} else {
		if len(errStr) > 0 {
			return errors.New("Stderr was full")
		}
	}
	return nil
}

func ExampleLvl2() {
	SetDebugVisible(2)
	stdToOs()
	Lvl1("Level1")
	Lvl2("Level2")
	Lvl3("Level3")
	Lvl4("Level4")
	Lvl5("Level5")
	stdToBuf()
	SetDebugVisible(1)

	// Output:
	// 1 : (                       log.ExampleLevel2:   0) - Level1
	// 2 : (                       log.ExampleLevel2:   0) - Level2
}

func ExampleLvl1() {
	stdToOs()
	Lvl1("Multiple", "parameters")
	stdToBuf()

	// Output:
	// 1 : (                  log.ExampleMultiParams:   0) - Multiple parameters
}

func ExampleLLvl1() {
	stdToOs()
	Lvl1("Lvl output")
	LLvl1("LLvl output")
	Lvlf1("Lvlf output")
	LLvlf1("LLvlf output")
	stdToBuf()

	// Output:
	// 1 : (                         log.ExampleLLvl:   0) - Lvl output
	// 1!: (                         log.ExampleLLvl:   0) - LLvl output
	// 1 : (                         log.ExampleLLvl:   0) - Lvlf output
	// 1!: (                         log.ExampleLLvl:   0) - LLvlf output
}

func thisIsAVeryLongFunctionNameThatWillOverflow() {
	stdToOs()
	Lvl1("Overflow")
}

func ExampleLvlf1() {
	stdToOs()
	Lvl1("Before")
	thisIsAVeryLongFunctionNameThatWillOverflow()
	Lvl1("After")
	stdToBuf()

	// Output:
	// 1 : (                log.ExampleLongFunctions:   0) - Before
	// 1 : (log.thisIsAVeryLongFunctionNameThatWillOverflow:   0) - Overflow
	// 1 : (                       log.ExampleLongFunctions:   0) - After
}

func ExampleLvl2() {
	NamePadding = -1
	stdToOs()
	Lvl1("Before")
	thisIsAVeryLongFunctionNameThatWillOverflow()
	Lvl1("After")
	stdToBuf()

	// Output:
	// 1 : (log.ExampleLongFunctionsLimit:   0) - Before
	// 1 : (log.thisIsAVeryLongFunctionNameThatWillOverflow:   0) - Overflow
	// 1 : (log.ExampleLongFunctionsLimit:   0) - After
}
