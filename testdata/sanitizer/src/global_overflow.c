/* Reads past the end of a global array. */
#include <stdio.h>

int limits[4] = {10, 20, 30, 40};

int lookup_limit(int level)
{
	return limits[level];
}

int main(int argc, char **argv)
{
	(void)argv;
	printf("%d\n", lookup_limit(argc + 3));
	return 0;
}
