package fixture

import "testing"

func TestMain(m *testing.M) { panic("discovery must not run the package") }

func TestCounted(t *testing.T)        {}
func Test_underscore(t *testing.T)    {}
func Testlowercase(t *testing.T)      {}
func (Suite) TestMethod(t *testing.T) {}
func FuzzParse(f *testing.F)          {}
func ExampleSuite() {
	// Output:
}

func ExampleSuite_silent()        {}
func BenchmarkSpeed(b *testing.B) {}
