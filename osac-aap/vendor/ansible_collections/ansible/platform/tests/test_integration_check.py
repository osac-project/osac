#!/usr/bin/env python

import os
from sys import exit

base_dir = os.path.abspath(os.path.join(os.path.dirname(__file__), os.pardir))
modules_that_need_development = ['authenticator_users']
tests_to_ignore = ['lookup_test', 'setup_gateway']


def get_files(dir_name):
    return [f for f in os.listdir(dir_name) if os.path.isfile(os.path.join(dir_name, f))]


def get_dirs(dir_name):
    return [f for f in os.listdir(dir_name) if os.path.isdir(os.path.join(dir_name, f))]


plugins = get_files(os.path.join(base_dir, 'plugins', 'modules'))
tests = get_dirs(os.path.join(base_dir, 'tests', 'integration', 'targets'))
for test_name in tests_to_ignore:
    tests.remove(test_name)

missing_tests = []
for plugin in plugins:
    plugin = plugin.replace('.py', '')
    if plugin[-1] != 's':
        plugin = f'{plugin}s'
    # If we every have something like inventory we will need to update this for `ies``.

    test_name = f'{plugin}_test'
    if test_name not in tests:
        missing_tests.append(plugin)
    else:
        tests.remove(test_name)

exit_code = 0
if missing_tests:
    print("Missing a test for the following plugins:")
    for test_name in missing_tests:
        if test_name in modules_that_need_development:
            print(f'    {test_name} [OK, needs development]')
        else:
            print(f'    {test_name}')
            exit_code = 1

if tests:
    print("We have tests for no plugins:")
    for test_name in tests:
        print(f'    {test_name}')
    exit_code = 1

exit(exit_code)
