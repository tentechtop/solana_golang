# Solana BPF 风格字节码 VM MVP 计划

## 目标边界

MVP 的目标是实现一个可执行自定义字节码的智能合约虚拟机，风格参考 Solana BPF/eBPF，但不兼容 EVM，也不要求完整兼容 Solana SBF。

MVP 必须满足：

- 能从程序账户加载字节码。
- 能在确定性沙箱中执行字节码。
- 能通过 Compute Unit 限制执行成本。
- 能安全读写 VM 内存和授权账户数据。
- 能通过白名单 syscall 访问运行时能力。
- 能返回日志、返回数据、账户变更和明确错误。
- 能用单元测试覆盖主要正常路径和边界路径。

MVP 暂不实现：

- JIT。
- 并发执行。
- 完整 eBPF 指令兼容。
- 跨合约调用完整语义。
- 动态链接。
- 合约在线升级治理。
- Rust/Go 高级语言直接编译到 VM 字节码。

## 当前项目基础

当前 `vm` 包已经具备最小执行骨架：

- `Runtime`：统一加载、执行、账户快照和结果返回。
- `BytecodeExecutor`：执行当前简化 opcode。
- `ComputeMeter`：限制 Compute Unit。
- `AccountSet`：控制账户读写权限。
- `SyscallRegistry`：提供 syscall 白名单入口。
- `programs/vm`：把链上 instruction 映射到 VM invocation。

后续 MVP 不应推翻现有结构，而是在现有结构上升级为寄存器式字节码 VM。

## 字节码如何编译

第一阶段不要直接编译 Go/Rust，也不要引入 LLVM。MVP 使用自研汇编器生成字节码：

```text
合约源码 .svmasm
    |
    v
cmd/svmasm 汇编器
    |
    v
VM 字节码 .svmbin
    |
    v
程序账户 Data
    |
    v
vm.Runtime 执行
```

原因：

- 自定义 ISA 尚未稳定，直接接 LLVM 成本高。
- 汇编器最容易做确定性测试和边界测试。
- 字节码格式可控，方便 verifier 做静态校验。
- 不引入第三方依赖，符合当前项目约束。

第二阶段再考虑高级语言：

```text
小型合约 DSL -> IR -> VM 字节码
```

第三阶段才考虑：

```text
Rust/C -> LLVM IR -> 自定义后端或 eBPF 子集 -> VM 字节码
```

## MVP 字节码格式

建议使用固定头部和固定宽度指令，降低解析复杂度。

```text
Magic      4 字节，固定为 SVM1
Version    2 字节
Flags      2 字节
CodeSize   4 字节
DataSize   4 字节
Code       N 字节
ReadOnly   M 字节
Checksum   32 字节
```

边界限制：

- 最大字节码大小：1 MiB。
- 最大只读数据：64 KiB。
- 最大指令数：64K。
- 指令必须固定宽度，禁止跳入指令中间。
- 所有数值使用 little-endian。

## MVP 指令集

寄存器：

```text
R0      返回值
R1-R5   syscall 参数
R6-R9   通用寄存器
R10     栈指针，受限写入
PC      程序计数器
```

第一批指令：

```text
EXIT
MOV_REG
MOV_IMM
ADD_REG
ADD_IMM
SUB_REG
SUB_IMM
MUL_REG
DIV_REG
LOAD
STORE
JMP
JZ
JNZ
SYSCALL
```

暂不实现：

```text
CALL
RET
复杂栈帧
浮点数
原子操作
未对齐大块内存访问
```

## 内存模型

MVP 内存分区：

```text
Code       只读
ReadOnly   只读
Stack      可读写
Heap       可读写，固定大小
Input      只读
Accounts   通过 AccountSet 受控访问
```

所有内存访问必须经过统一接口：

```text
ReadMemory(address, size)
WriteMemory(address, data)
```

必须检查：

- 地址是否越界。
- 地址加长度是否整数溢出。
- 区域是否允许读写。
- 长度是否超过单次访问上限。
- 栈和堆是否越界。

## Verifier 静态校验

执行前必须校验：

- 文件头合法。
- 版本号受支持。
- code/data 长度合法。
- checksum 正确。
- opcode 合法。
- 立即数格式合法。
- 跳转目标合法。
- 跳转目标必须对齐到指令边界。
- syscall ID 必须在白名单中。
- 禁止写只读段。
- 禁止使用保留寄存器进行非法写入。

Verifier 失败必须拒绝执行，不能进入解释器。

## Compute Unit 计费

所有执行路径必须扣费。

建议初始成本：

```text
基础指令      1
MUL/DIV       3
LOAD          2
STORE         4
JMP/JZ/JNZ    1
SYSCALL       基础 20 + syscall 自身成本
```

规则：

- 扣费发生在指令执行前。
- Compute Unit 不足立即失败。
- syscall 内部按输入长度和操作复杂度继续扣费。
- 失败后由上层丢弃本次执行结果。

## Syscall MVP

第一批 syscall：

```text
Log
Sha256
GetClock
SetReturnData
GetAccountData
SetAccountData
```

严格禁止：

- 读取系统真实时间。
- 读取环境变量。
- 访问文件系统。
- 访问网络。
- 创建线程。
- 调用宿主未注册函数。

## 汇编器 MVP

新增命令：

```text
cmd/svmasm
```

输入示例：

```text
mov r1, 100
mov r2, 23
add r1, r2
syscall log
exit
```

输出：

```text
contract.svmbin
```

汇编器职责：

- 解析 `.svmasm` 文本。
- 校验寄存器名称。
- 校验立即数范围。
- 解析 label。
- 生成固定宽度指令。
- 生成字节码文件头。
- 输出 checksum。

汇编器不负责：

- 高级语言语法。
- 类型推导。
- 优化。
- 链接多个文件。

## 开发阶段

### 第一阶段：字节码基础

- 定义新字节码文件头。
- 定义寄存器和 opcode。
- 实现 decoder。
- 实现 verifier。
- 保留旧 `BytecodeExecutor` 测试，新增寄存器式 executor。

验收标准：

- 非法 magic 被拒绝。
- 非法 opcode 被拒绝。
- 非法跳转被拒绝。
- 合法 EXIT 程序可执行成功。

### 第二阶段：解释器

- 实现寄存器文件。
- 实现 PC 控制。
- 实现算术指令。
- 实现跳转指令。
- 实现 Compute Unit 扣费。

验收标准：

- 算术程序返回正确 R0。
- 死循环被 Compute Unit 截断。
- 除零返回明确错误。

### 第三阶段：内存

- 实现内存分区。
- 实现 LOAD/STORE。
- 实现只读段保护。
- 实现栈和堆边界检查。

验收标准：

- 合法内存读写成功。
- 越界读写失败。
- 写只读段失败。
- 地址溢出失败。

### 第四阶段：syscall

- 接入现有 `SyscallRegistry`。
- 实现寄存器参数传递。
- 实现 Log、Sha256、GetClock、SetReturnData。
- 实现账户数据读写 syscall。

验收标准：

- syscall 白名单外调用失败。
- Log 输出进入 Result。
- Sha256 输出确定。
- 未授权账户写入失败。

### 第五阶段：汇编器

- 新增 `cmd/svmasm`。
- 支持基础指令。
- 支持 label。
- 支持输出 `.svmbin`。
- 为汇编器增加单元测试。

验收标准：

- `.svmasm` 可编译为 `.svmbin`。
- `.svmbin` 可被 VM 加载执行。
- 非法寄存器、非法立即数、重复 label 均失败。

### 第六阶段：集成测试

- 合约字节码写入程序账户。
- 通过 `programs/vm` 调用 VM。
- 成功写回授权账户。
- 失败不写回账户状态。

验收标准：

- 单元测试覆盖正常路径。
- 单元测试覆盖非法路径。
- 不修改 `openapi.yml`。

## 建议目录

```text
vm/
  bytecode_format.go
  opcode.go
  decoder.go
  verifier.go
  register_file.go
  memory.go
  interpreter.go
  assembler_test.go
  interpreter_test.go
  verifier_test.go

cmd/svmasm/
  main.go

vm/assembler/
  parser.go
  assembler.go
  encoder.go
  labels.go
```

工具类通用方法如字节克隆、边界计算、checksum 可放入 `utils`，但只有多个包复用时才抽取，避免过早抽象。

## 自查标准

每个阶段完成后必须检查：

- 代码嵌套不超过 3 层。
- 所有边界检查都在访问前完成。
- 错误返回带上下文。
- syscall 无默认放行路径。
- 失败路径不返回部分状态。
- 测试覆盖主要边界。
- 不引入无必要第三方依赖。
- 不修改 `openapi.yml`。
