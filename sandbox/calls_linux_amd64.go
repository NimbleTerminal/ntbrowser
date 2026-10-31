package sandbox

const (
	sysExecveat        = 322
	sysInitModule      = 175
	sysFinitModule     = 313
	sysBPF             = 321
	sysKexecLoad       = 246
	sysProcessVMReadv  = 310
	sysProcessVMWritev = 311
	sysSocket          = 41
	sysSeccomp         = 317
)

const sysKill = 62

// metadataCalls change a file without opening it, which landlock cannot see.
var metadataCalls = []uintptr{
	76,          // truncate
	90, 91, 268, // chmod, fchmod, fchmodat
	sysFchmodat2,    //
	92, 93, 94, 260, // chown, fchown, lchown, fchownat
	188, 189, 190, // setxattr, lsetxattr, fsetxattr
	197, 198, 199, // removexattr, lremovexattr, fremovexattr
	132, 235, 261, 280, // utime, utimes, futimesat, utimensat
}
