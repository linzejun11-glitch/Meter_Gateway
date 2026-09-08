package device

// Fake 是不连接真实硬件的模拟电表。
//
// 它主要供单元测试使用：测试可以预先填写返回数据和错误，再检查采集器或
// 控制器是否正确调用了电表接口。Fake 的英文含义是“模拟的、假的”。
type Fake struct {
	Measurements       Measurements
	MeasurementsError  error
	Switches           SwitchSnapshot
	SwitchesError      error
	Info               Info
	InfoError          error
	ControlActualState bool
	ControlStateKnown  bool
	ControlError       error

	ControlCalls []ControlCall
	Closed       bool
}

// ControlCall 记录一次控制调用，供测试断言通道和目标状态。
type ControlCall struct {
	Channel int
	State   bool
}

func (f *Fake) ReadMeasurements() (Measurements, error) {
	return f.Measurements, f.MeasurementsError
}

func (f *Fake) ReadSwitches() (SwitchSnapshot, error) {
	return f.Switches, f.SwitchesError
}

func (f *Fake) ControlDigitalOutput(
	channel int,
	desiredState bool,
) (bool, bool, error) {
	f.ControlCalls = append(f.ControlCalls, ControlCall{
		Channel: channel,
		State:   desiredState,
	})
	return f.ControlActualState, f.ControlStateKnown, f.ControlError
}

func (f *Fake) ReadInfo() (Info, error) {
	return f.Info, f.InfoError
}

func (f *Fake) Close() error {
	f.Closed = true
	return nil
}

// 编译期检查：Fake漏掉Device要求的任何方法时，编译器会在这里报错。
var _ Device = (*Fake)(nil)
