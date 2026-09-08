# 电表边缘网关

这是一个使用 Go 编写的电表边缘网关。程序在现场侧通过串口和
Modbus-RTU 与电表通信，在服务器侧通过 MQTT 与 Python 服务通信。

当前已经实现 QS300 驱动。上层采集、控制和通信代码只依赖统一电表接口，
以后接入其他品牌或型号时，不需要复制整套网关程序。

## 主要功能

- 定时读取 A 相电压、电流和有功功率；
- 读取两路 DI 和两路 DO；
- 接收服务器下发的 DO1、DO2 控制命令；
- 使用功能码 05 写线圈，并轮询状态寄存器确认实际状态；
- 使用 MQTT 3.1.1、QoS 0 和设备名/密码认证；
- 使用 12 字节自定义帧头、CRC16-CCITT 和 JSON 属性上报；
- 支持单帧、多帧、选择重传和全部重传；
- 等待 MQTT 连接和 Topic 订阅完成后再开始采集；
- 使用 Context 和 WaitGroup 安全停止任务并关闭串口；
- 支持使用模拟电表完成不依赖硬件和服务器的单元测试。

## 项目结构

```text
MOCK_COLLECT/
├─ gateway/
│  └─ cmd/                    正式网关程序入口
├─ meter/
│  ├─ app/                    组装模块、启动顺序、连接就绪和优雅退出
│  ├─ cmd/                    备用入口，与正式入口运行同一套电表应用
│  ├─ collector/              周期采集和状态上报
│  ├─ controller/             DO控制、结果确认和ACK缓存
│  ├─ device/                 统一电表接口、统一数据类型和模拟电表
│  ├─ driver/                 根据配置选择具体电表驱动
│  │  └─ qs300/               QS300寄存器、单位换算和DI/DO控制规则
│  ├─ modbus/                 通用Modbus-RTU报文、串口收发和CRC16
│  └─ transport/              上层使用的统一网络通信接口
├─ common/
│  ├─ config/                 驱动、串口、MQTT和采集周期配置
│  ├─ mqttclient/             MQTT、JSON、分帧和重传
│  └─ protocol/               业务消息、MQTT帧和CRC16-CCITT
├─ go.mod
├─ go.sum
└─ README.md
```

## 分层关系

```text
gateway/cmd
    ↓
meter/app（组装与生命周期）
    ├─ collector ─┐
    ├─ controller ├─→ device.Device（统一电表接口）
    │             │        ↓
    │             │   driver/qs300（型号规则）
    │             │        ↓
    │             │   modbus（通用RTU通信）→ COM口 → 电表
    │             │
    └─ transport.Client（统一网络接口）→ mqttclient → MQTT服务器
```

这次拆分的关键是依赖方向：采集器和控制器认识 `device.Device`，但不认识
QS300、寄存器地址或串口库。因此更换电表时，主要变化被限制在 `meter/driver`
下面，上层业务消息和 MQTT 链路可以继续复用。

## 默认配置

```text
电表驱动：qs300
串口：COM3
串口参数：9600 8 N 1
Modbus Slave ID：1
Modbus超时：3秒
采集周期：8秒
MQTT Broker：tcp://192.168.54.150:1883
MQTT设备名：dev1
MQTT密码：123456
属性回复超时：10秒
属性分片大小：30字节
最大重传轮数：3
```

可通过环境变量选择电表驱动和覆盖 MQTT 参数：

```powershell
$env:METER_DRIVER="qs300"
$env:TRANSPORT="mqtt"
$env:MQTT_BROKER="tcp://192.168.54.150:1883"
$env:MQTT_DEVICE_NAME="dev1"
$env:MQTT_PASSWORD="设备实际密码"
```

## 运行

运行前关闭 SSCOM、ModbusTool 等占用 COM3 的程序。

正式运行：

```powershell
go run ./gateway/cmd
```

备用入口：

```powershell
go run ./meter/cmd
```

两个入口运行的是同一套应用，都会使用同一串口和同一 MQTT Client ID，
因此不能同时运行。

## 无设备测试

以下命令不会启动网关入口，不会打开 COM3，也不会连接 MQTT 服务器：

```powershell
go test ./...
go vet ./...
```

单元测试使用 `meter/device.Fake` 和 QS300 的内存假总线验证：

- 原始寄存器值的单位换算；
- 310、311 寄存器的 DI/DO 位解析；
- DO1/DO2 到线圈 0/1 的地址映射；
- 电表状态延迟刷新时的轮询确认；
- 状态不一致时的统一错误；
- 采集数据和控制命令能否正确穿过统一接口。

本地编译同样不会运行程序或访问设备：

```powershell
go build -o ./gateway.exe ./gateway/cmd
go build -o ./meter.exe ./meter/cmd
```

## MQTT Topic

```text
$sys/{device}/post          电表属性上报
$sys/{device}/post/reply    上报确认和重传命令
$sys/{device}/set           DO控制命令
$sys/{device}/set/reply     DO控制结果
```

## DO通道映射

```text
JSON do1 → channel=1 → QS300物理DO0 → 端子15/16 → Modbus线圈0
JSON do2 → channel=2 → QS300物理DO1 → 端子17/18 → Modbus线圈1
```

控制命令的 id 必须是 13 位数字字符串，每条命令只能控制一路：

```json
{"id":"0000000000001","params":{"do1":true}}
{"id":"0000000000002","params":{"do2":"0"}}
```

DO 值兼容 `true/false`、数字 `0/1` 和字符串 `"0"/"1"`。

## QS300寄存器

这些地址只属于 QS300，因此保存在 `meter/driver/qs300`，没有放进通用
Modbus 层：

```text
70   A相电压
76   A相电流
79   A相有功功率
301  Modbus从站地址
304  波特率编码
305  串口格式编码
310  DO状态
311  DI状态
```

测量单位：

```text
voltage       V
current       mA
active_power  W
```

## 接入新品牌电表

1. 在 `meter/driver` 下新建型号目录，例如 `meter/driver/brandx`；
2. 实现 `meter/device.Device` 接口，把该型号的寄存器和换算规则放入目录；
3. 在 `meter/driver/factory.go` 中注册新的 `METER_DRIVER` 名称；
4. 为新驱动编写内存假总线测试；
5. 最后再使用真实设备做串口参数、寄存器表和接线端子的现场确认。

## 无硬件验证的边界

单元测试和编译可以证明软件分层、数据转换和控制流程正确，但无法替代以下
现场验证：厂商寄存器手册是否与实际固件一致、RS-485 接线及串口参数、继电器
端子的电气特性，以及真实 MQTT Broker 的认证和网络环境。设备和服务器恢复后，
仍应补做一次完整端到端联调。
