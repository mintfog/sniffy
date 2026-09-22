// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

#include <libproc.h>
#include <stddef.h>

#define SIZE(type, expected) _Static_assert(sizeof(struct type) == expected, #type)
#define OFFSET(type, field, expected) _Static_assert(offsetof(struct type, field) == expected, #type "." #field)
SIZE(socket_fdinfo, darwinSocketSize);
SIZE(proc_bsdinfo, darwinBSDSize);
SIZE(proc_fdinfo, darwinFDSize);
OFFSET(socket_fdinfo, psi.soi_protocol, darwinProtocolOffset);
OFFSET(socket_fdinfo, psi.soi_family, darwinFamilyOffset);
OFFSET(socket_fdinfo, psi.soi_kind, darwinKindOffset);
OFFSET(socket_fdinfo, psi.soi_proto.pri_tcp.tcpsi_ini, darwinInetOffset);
OFFSET(socket_fdinfo, psi.soi_proto.pri_tcp.tcpsi_state, darwinStateOffset);
OFFSET(in_sockinfo, insi_fport, darwinForeignPortOffset);
OFFSET(in_sockinfo, insi_lport, darwinLocalPortOffset);
OFFSET(in_sockinfo, insi_vflag, darwinVersionOffset);
OFFSET(in_sockinfo, insi_faddr, darwinForeignAddrOffset);
OFFSET(in_sockinfo, insi_laddr, darwinLocalAddrOffset);
OFFSET(in_sockinfo, insi_v6.in6_ifindex, darwinInterfaceOffset);
OFFSET(proc_bsdinfo, pbi_uid, darwinUIDOffset);
OFFSET(proc_bsdinfo, pbi_comm, darwinCommOffset);
OFFSET(proc_bsdinfo, pbi_name, darwinNameOffset);
_Static_assert(PROC_PIDLISTFDS == procPIDListFDs, "PROC_PIDLISTFDS");
_Static_assert(PROC_PIDTBSDINFO == procPIDTBSDInfo, "PROC_PIDTBSDINFO");
_Static_assert(PROC_PIDFDSOCKETINFO == procPIDFDSocketInfo, "PROC_PIDFDSOCKETINFO");
_Static_assert(PROX_FDTYPE_SOCKET == procFDSocket, "PROX_FDTYPE_SOCKET");
_Static_assert(SOCKINFO_TCP == socketInfoTCP, "SOCKINFO_TCP");
_Static_assert(TSI_S_ESTABLISHED == darwinTCPEstablished, "TSI_S_ESTABLISHED");
_Static_assert(TSI_S_FIN_WAIT_2 == darwinTCPLastConnected, "TSI_S_FIN_WAIT_2");
