#!/usr/bin/env python3
# -*- coding: utf-8 -*-

print("这是一个测试输入的脚本")
print("This is a script that tests input handling")

# Test basic input
name = input("请输入您的名字 (Please enter your name): ")
print(f"您好, {name}! (Hello, {name}!)")

# Test numeric input
age = input("请输入您的年龄 (Please enter your age): ")
print(f"您的年龄是 {age} (Your age is {age})")

# Test calculation
try:
    num1 = float(input("请输入第一个数字 (Enter first number): "))
    num2 = float(input("请输入第二个数字 (Enter second number): "))
    print(f"{num1} + {num2} = {num1 + num2}")
except ValueError:
    print("输入的不是有效数字 (Invalid number input)")

print("脚本执行完成! (Script execution completed!)")