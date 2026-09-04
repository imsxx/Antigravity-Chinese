@echo off
chcp 65001 >nul
title Antigravity 官方原版英文一键还原工具
echo =======================================================
echo          Antigravity 官方原版英文一键还原工具
echo =======================================================
echo.
python "%~dp0patch_settings.py" --restore
echo.
pause
