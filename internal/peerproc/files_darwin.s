#include "textflag.h"

TEXT libproc_proc_pidinfo_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libproc_proc_pidinfo(SB)
GLOBL	·libproc_proc_pidinfo_trampoline_addr(SB), RODATA, $8
DATA	·libproc_proc_pidinfo_trampoline_addr(SB)/8, $libproc_proc_pidinfo_trampoline<>(SB)

TEXT libproc_proc_pidfdinfo_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libproc_proc_pidfdinfo(SB)
GLOBL	·libproc_proc_pidfdinfo_trampoline_addr(SB), RODATA, $8
DATA	·libproc_proc_pidfdinfo_trampoline_addr(SB)/8, $libproc_proc_pidfdinfo_trampoline<>(SB)
