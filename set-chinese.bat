@echo off
chcp 65001 >nul
title Antigravity 设置界面一键中文化工具
echo =======================================================
echo          Antigravity 设置界面一键中文化工具
echo =======================================================
echo.
python "%~dp0patch_settings.py"
echo.
pause
