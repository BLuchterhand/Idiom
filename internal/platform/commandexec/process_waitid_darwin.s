//go:build darwin

#include "textflag.h"

TEXT libc_waitid_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_waitid(SB)
GLOBL	·libcWaitidAddr(SB), RODATA, $8
DATA	·libcWaitidAddr(SB)/8, $libc_waitid_trampoline<>(SB)
