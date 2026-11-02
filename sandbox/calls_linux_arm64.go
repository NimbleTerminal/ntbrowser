// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package sandbox

const (
	sysExecveat        = 281
	sysInitModule      = 105
	sysFinitModule     = 273
	sysBPF             = 280
	sysKexecLoad       = 104
	sysProcessVMReadv  = 270
	sysProcessVMWritev = 271
	sysSocket          = 198
	sysSeccomp         = 277
)

const sysKill = 129

// metadataCalls change a file without opening it, which landlock cannot see.
var metadataCalls = []uintptr{
	45,     // truncate
	52, 53, // fchmod, fchmodat
	sysFchmodat2, //
	54, 55,       // fchownat, fchown
	5, 6, 7, // setxattr, lsetxattr, fsetxattr
	14, 15, 16, // removexattr, lremovexattr, fremovexattr
	88, // utimensat
}
