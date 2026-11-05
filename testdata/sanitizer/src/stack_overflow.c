/* Writes one element past the end of an array on the stack. */
#include <stdio.h>

void fill(int *a, int n)
{
	for (int i = 0; i < n; i++)
		a[i] = i * i;
}

int sum_window(int n)
{
	int window[8];
	fill(window, n);
	int s = 0;
	for (int i = 0; i < 8; i++)
		s += window[i];
	return s;
}

int main(int argc, char **argv)
{
	(void)argv;
	printf("%d\n", sum_window(8 + argc));
	return 0;
}
