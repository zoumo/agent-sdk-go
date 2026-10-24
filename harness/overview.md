---
type: Architecture
title: 项目架构总览
status: stable
---

# 项目架构总览

[SDK 通用架构](sdk/architecture.md)规定长期 Session、原生事件和共同控制的目标合同；[共同用户故事](sdk/user-stories.md)规定所有接入方的验收目标；[接入流程](sdk/onboarding.md)规定实现与交付阶段。

项目采用单一模块与版本，原生 SDK 保持协议与运行语义独立。模块和依赖边界见[架构规范](architecture.md)，职责布局见[代码布局](code-layout.md)，工程验收见[测试规则](testing.md)。

[术语表](glossary.md)统一证据与关系语言；[设计决定](adr/index.md)记录已采纳的边界和取舍。

[具体合同](contracts/index.md)维护共享机制与原生适配的详细约束，包括关系、事件交付和历史加载。具体合同与通用目标应分别核对，不能把既有实现约束或历史验收结果当作已经满足目标架构的证明。发现冲突须记录并修订，不得在验收时任意选择较宽松的语义。

规范与合同位于 harness。原生协议来源、录制、实现说明、验证结果、架构检查报告和发布操作材料在外部维护，并反向引用这里的合同。
