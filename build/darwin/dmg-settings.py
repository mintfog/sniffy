# Copyright 2026 The mintfog Authors
# SPDX-License-Identifier: Apache-2.0

from pathlib import Path

application = Path(defines["app"])
files = [str(application)]
symlinks = {"Applications": "/Applications"}
icon = str(application / "Contents/Resources/appicon.icns")
background = defines["background"]
format = "UDZO"
filesystem = "HFS+"

# 坐标与 720 × 460 的背景对齐，Finder 自动排列会破坏拖拽引导的顺序。
window_rect = ((200, 120), (720, 460))
default_view = "icon-view"
arrange_by = None
icon_locations = {"Sniffy.app": (180, 270), "Applications": (540, 270)}
icon_size = 128
text_size = 14
label_pos = "bottom"
hide_extensions = ["Sniffy.app"]
show_status_bar = False
show_tab_view = False
show_toolbar = False
show_pathbar = False
show_sidebar = False
show_icon_preview = False
show_item_info = False
include_icon_view_settings = True
include_list_view_settings = False
