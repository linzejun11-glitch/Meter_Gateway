# 电表边缘网关

这是一个使用Go编写的多电表边缘网关。多块电表使用不同Modbus Slave ID
挂在同一条RS485总线上，网关只打开一次串口，并通过一条MQTT连接与Python
服务器通信。

当前实现QS300驱动。采集和控制层只依赖统一电表接口，因此以后可以继续增加
其他品牌驱动。

## 数据链路

```text
QS300（Slave ID 1）──┐
QS300（Slave ID 2）──┼── 同一条RS485总线 ── COM3
更多电表……          ──┘             ↓
                              Go电表网关
                                   ↓
                       一条MQTT连接 / 共享Topic
                                   ↓
                            Python MQTT服务器
```

所有电表可以拥有独立采集周期，但底层RS485事务由同一个互斥锁串行执行，
不会出现两个请求同时写入串口的情况。

## 项目结构

```text
MOCK_COLLECT/
├─ .github/workflows/ci.yml    GitHub自动测试、静态检查和编译
├─ config.yaml                 RS485、电表列表和MQTT配置
├─ gateway/cmd/                正式程序入口
├─ meter/
│  ├─ app/                     多设备组装、启动、就绪等待和优雅退出
│  ├─ collector/               每块电表的周期采集任务
│  ├─ controller/              按meter_id路由DO控制命令
│  ├─ device/                  统一电表接口和模拟电表
│  ├─ driver/
│  │  └─ qs300/                QS300寄存器、换算和DO规则
│  ├─ modbus/                  共享RS485总线和通用Modbus-RTU
│  └─ transport/               统一网络通信接口
└─ common/
   ├─ config/                  YAML解析、环境变量覆盖和配置校验
   ├─ mqttclient/              MQTT、分帧、回复和重传
   └─ protocol/                网关内部业务消息
```

## YAML配置

程序默认读取当前目录的`config.yaml`。最小示例：

```yaml
transport: mqtt

rs485:
  port: COM3
  baud_rate: 9600
  data_bits: 8
  parity: N
  stop_bits: 1
  timeout: 3s

meters:
  - meter_id: 1
    name: meter001
    driver: qs300
    slave_id: 1
    collect_interval: 8s

  - meter_id: 2
    name: meter002
    driver: qs300
    slave_id: 2
    collect_interval: 10s

mqtt:
  broker: tcp://192.168.54.150:1883
  device_name: dev1
  password: "123456"
  reply_timeout: 10s
  chunk_size: 30
  max_retransmits: 3
```

字段含义：

- `meter_id`：服务器使用的业务编号，必须唯一；
- `slave_id`：电表现场Modbus地址，同一条RS485总线上必须唯一；
- `name`：日志中方便人识别的名称；
- `driver`：型号驱动名称，目前是`qs300`；
- `collect_interval`：该电表自己的采集周期。

业务编号和Slave ID故意分开。例如现场将Slave ID从2调整成5时，只需修改
`slave_id`，服务器中的`meter_id`可以保持不变。

使用其他配置文件：

```powershell
$env:GATEWAY_CONFIG="C:\gateway\production.yaml"
go run ./gateway/cmd
```

以下网络配置仍可以用环境变量覆盖：

```powershell
$env:MQTT_BROKER="tcp://192.168.54.150:1883"
$env:MQTT_DEVICE_NAME="dev1"
$env:MQTT_PASSWORD="实际密码"
```

YAML中出现未知字段、重复`meter_id`、重复`slave_id`或非法时长时，程序会在
打开串口前直接报错。

## 多设备MQTT控制

整个网关只建立一个MQTT连接，多块电表共用以下Topic：

```text
$sys/{gateway_device_name}/post
$sys/{gateway_device_name}/post/reply
$sys/{gateway_device_name}/set
$sys/{gateway_device_name}/set/reply
```

多块电表时，下行命令必须在JSON顶层提供`meter_id`：

```json
{
  "id": "0000000000001",
  "meter_id": 2,
  "params": {
    "do1": true
  }
}
```

命令含义是：控制业务编号为2的电表，将DO1闭合。

控制回复也会带回相同的`meter_id`：

```json
{"id":"0000000000001","meter_id":2,"code":0,"msg":"success"}
```

每条命令只能包含`do1`或`do2`中的一个。值兼容`true/false`、数字`0/1`
和字符串`"0"/"1"`。

为了兼容现有单表服务器，配置中只有一块电表时，命令可以省略`meter_id`；
配置两块或更多电表后，省略它会被拒绝。

## 运行

运行前关闭SSCOM、ModbusTool等占用串口的程序：

```powershell
go run ./gateway/cmd
```

备用入口运行的是同一套应用，不能和正式入口同时运行：

```powershell
go run ./meter/cmd
```

## 无设备测试

普通测试不会打开COM口，也不会连接MQTT服务器：

```powershell
go test -count=1 ./...
go vet ./...
go build -o gateway.exe ./gateway/cmd
go build -o meter.exe ./meter/cmd
```

当前自动测试覆盖：

- YAML解析、未知字段和重复编号检查；
- 两个Slave ID共享一条RS485总线且事务不会重叠；
- QS300寄存器、单位换算、DI/DO映射和控制确认；
- 两块模拟电表同时启动、分别上报和优雅退出；
- MQTT多设备命令路由、未连接错误、自动重连配置；
- 控制命令路由、ACK缓存、分帧和重传算法。

推送到GitHub后，`.github/workflows/ci.yml`会自动运行测试、`go vet`和两个
Windows程序的编译。

需要真实Python服务器的测试默认跳过。服务器可用后，可以显式启用：

```powershell
$env:PYTHON_MQTT_INTEGRATION="1"
go test ./common/mqttclient -run Integration -v
```

## QS300通道和寄存器

```text
JSON do1 → channel=1 → QS300物理DO0 → 端子15/16 → 线圈0
JSON do2 → channel=2 → QS300物理DO1 → 端子17/18 → 线圈1

70   A相电压
76   A相电流
79   A相有功功率
301  Modbus从站地址
304  波特率编码
305  串口格式编码
310  DO状态
311  DI状态
```

## 接入其他品牌

1. 在`meter/driver`下创建新型号目录；
2. 实现`meter/device.Device`接口；
3. 将型号寄存器和换算规则保存在新驱动中；
4. 在`meter/driver/factory.go`注册驱动名；
5. 先使用内存假总线编写测试，再进行真实设备联调。

## 无硬件验证的边界

自动测试能够验证程序结构、并发保护、数据转换和命令路由，但不能证明真实
设备的Slave ID、RS485接线、寄存器手册、继电器端子和实际固件完全一致。
设备和服务器恢复后，仍需补做一次完整端到端联调。
