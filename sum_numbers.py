#!/usr/bin/env python3
"""
Simple script to sum two numbers.
"""


def sum_two_numbers(a, b):
    """
    Sum two numbers and return the result.

    Args:
        a: First number
        b: Second number

    Returns:
        The sum of a and b
    """
    return a + b


def main():
    """Main function to demonstrate summing two numbers."""
    # Example usage
    num1 = 5
    num2 = 10
    result = sum_two_numbers(num1, num2)
    print(f"The sum of {num1} and {num2} is: {result}")

    # Interactive mode - get input from user
    try:
        num1 = float(input("Enter the first number: "))
        num2 = float(input("Enter the second number: "))
        result = sum_two_numbers(num1, num2)
        print(f"The sum of {num1} and {num2} is: {result}")
    except ValueError:
        print("Error: Please enter valid numbers")
    except KeyboardInterrupt:
        print("\nExiting...")


if __name__ == "__main__":
    main()
